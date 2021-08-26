/*
 * stalld: starvation detection and avoidance (with bounds).
 *
 * This program was born after Daniel and Juri started debugging once again
 * problems caused kernel threads starving due to busy-loop sched FIFO threads.
 *
 * The idea is simple: after detecting a thread starving on a given CPU for a
 * given period, this thread will receive a "bounded" chance to run, using
 * SCHED_DEADLINE. In this way, the starving thread is able to make progress
 * causing a bounded Operating System noise (OS Noise).
 *
 * SPDX-License-Identifier: GPL-2.0
 *
 * Copyright (C) 2020 Red Hat Inc, Daniel Bristot de Oliveira <bristot@redhat.com>
 *
 */

#define _GNU_SOURCE
#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <getopt.h>
#include <pthread.h>
#include <sched.h>
#include <signal.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <syslog.h>
#include <sys/param.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <time.h>
#include <unistd.h>
#include <linux/sched.h>
#include <sys/file.h>
#include <regex.h>

#include "stalld.h"

/*
 * version
 */
const char *version = VERSION;

/*
 * logging.
 */
int config_verbose = 0;
int config_write_kmesg = 0;
int config_log_syslog = 1;
int config_log_only = 0;
int config_foreground = 0;

/*
 * denylisting feature
 */
int config_ignore = 0;

/*
 * boost parameters (time in nanoseconds).
 */
unsigned long config_dl_period  = 1000000000;
unsigned long config_dl_runtime = 20000;

/*
 * fifo boost parameters
 */
unsigned long config_fifo_priority = 98;
unsigned long config_force_fifo = 0;

/*
 * control loop (time in seconds)
 */
long config_starving_threshold = 30;
long config_boost_duration = 3;
long config_aggressive = 0;
long config_granularity = 5;

/*
 * XXX: Make it a cpu mask, lazy Daniel!
 */
int config_monitor_all_cpus = 1;
char *config_monitored_cpus;

/*
 * This will get set when we finish reading first time
 * in detect_task_format. May change over time as the
 * system gets loaded
 */
size_t config_buffer_size;

/*
 * auto-detected task format from sched_debug.
 */
int config_task_format;

/*
 * boolean for if running under systemd
 */
int config_systemd;

/*
 * boolean to choose between deadline and fifo
 */
int boost_policy;

/*
 * variable to indicate if stalld is running or
 * shutting down
 */
int running = 1;

/*
 * size of pages in bytes
 */
long page_size;

/*
 * config single threaded: uses less CPU, but has a lower precision.
 */
int config_single_threaded = 1;

/*
 * config adaptive multi-threaded: use a single thread when nothing
 * is happening, but dispatches a per-cpu thread after a starving
 * thread is waiting for half of the config_starving_threshold.
 */
int config_adaptive_multi_threaded = 0;

/*
 * check the idle time before parsing sched_debug
 */
int config_idle_detection = 1;
int STAT_MAX_SIZE = 4096;

/*
 * variables related to the threads to be ignored
 */
unsigned int nr_thread_ignore = 0;
regex_t *compiled_regex_thread = NULL;

/*
 * variables related to the processes to be ignored
 */
unsigned int nr_process_ignore = 0;
regex_t *compiled_regex_process = NULL;

/*
 * store the current sched_debug file path.
 */
char *config_sched_debug_path = NULL;

/*
 * API to fetch process name from process group ID
 */
char *get_process_comm(int tgid)
{
	char file_location[PROC_PID_FILE_PATH_LEN];
	char *process_name;
	FILE *fp;
	int n;

	process_name = calloc(COMM_SIZE + 1, sizeof(char));
	if (process_name == NULL)
		return NULL;

	n = sprintf(file_location, "/proc/%d/comm", tgid);
	if (n < 0)
		goto out_error;

	if ((fp = fopen(file_location, "r")) == NULL)
		goto out_error;

	if (fscanf(fp, "%s", process_name) != 1)
		goto out_close_fd;

	fclose(fp);
	return process_name;

out_close_fd:
	fclose(fp);
out_error:
	free(process_name);
	return NULL;
}

/*
 * API to fetch the process group ID for a thread/process
 */
int get_tgid(int pid)
{
	const char tgid_field[TGID_FIELD] = "Tgid:";
	char file_location[PROC_PID_FILE_PATH_LEN];
	char *status = NULL;
	int tgid, n;
	FILE *fp;

	status = calloc(TMP_BUFFER_SIZE, sizeof(char));
	if (status == NULL) {
		return -ENOMEM;
	}

	n = sprintf(file_location, "/proc/%d/status", pid);
	if (n < 0)
		goto out_free_mem;

	if ((fp = fopen(file_location, "r")) == NULL)
		goto out_free_mem;

	/*
	 * Iterate till we find the tgid field
	 */
	while (1) {
		if (fgets(status, TMP_BUFFER_SIZE, fp) == NULL)
			goto out_close_fd;
		if (!(strncmp(status, tgid_field, (TGID_FIELD - 1))))
			break;
		/*
		 * Zero out the buffer just in case
		 */
		memset(status, 0, TMP_BUFFER_SIZE);
	}
	/*
	 * since we're now at the line we're interested in,
	 * let's read in the field that we want
	 */
	if (sscanf(status, "%*s %d", &tgid) != 1)
		goto out_close_fd;

	fclose(fp);
	free(status);
	return tgid;

out_close_fd:
	fclose(fp);
out_free_mem:
	free(status);
	return -EINVAL;
}

/*
 * read the content of sched_debug into the input buffer.
 */
int read_sched_stat(char *buffer, int size)
{
	int position = 0;
	int retval;
	int fd;

	fd = open("/proc/stat", O_RDONLY);

	if (!fd)
		goto out_error;

	do {
		retval = read(fd, &buffer[position], size - position);
		if (retval < 0)
			goto out_close_fd;

		position += retval;

	} while (retval > 0 && position < size);

	buffer[position-1] = '\0';

	close(fd);

	return position;

out_close_fd:
	close(fd);

out_error:
	return 0;
}

/* format:
cpu1 832882 9111 153357 751780 456 32198 15356 0 0 0
cpu  user   nice system IDLE
*/
static long get_cpu_idle_time(char *buffer, size_t buffer_size, int cpu)
{
	char cpuid[10]; /* cpuXXXXX\n */
	char *idle_start;
	char *end;
	long val;

	sprintf(cpuid, "cpu%d ", cpu);

	/* CPU */
	idle_start = strstr(buffer, cpuid);
	if (!idle_start)
		return -ENODEV; /* cpu might be offline */

	/* find and skip space before user */
	idle_start = strstr(idle_start, " ");
	if (!idle_start)
		return -EINVAL;

	idle_start+=1;

	/* find and skip space before nice */
	idle_start = strstr(idle_start, " ");
	if (!idle_start)
		return -EINVAL;

	idle_start+=1;

	/* find and skip space before system */
	idle_start = strstr(idle_start, " ");
	if (!idle_start)
		return -EINVAL;

	idle_start+=1;

	/* Here is the idle! */
	idle_start = strstr(idle_start, " ");
	if (!idle_start)
		return -EINVAL;

	idle_start += 1;

	/* end */
	end = strstr(idle_start, " ");
	if (!end)
		return -EINVAL;

	errno = 0;
	val = strtol(idle_start, &end, 10);
	if (errno != 0)
		return -EINVAL;

	return val;
}

int cpu_had_idle_time(struct cpu_info *cpu_info)
{
	char sched_stat[STAT_MAX_SIZE];
	long idle_time;

	if (!read_sched_stat(sched_stat, STAT_MAX_SIZE)) {
		warn("fail reading sched stat file");
		warn("disabling idle detection");
		config_idle_detection = 0;
		return 0;
	}

	idle_time = get_cpu_idle_time(sched_stat, STAT_MAX_SIZE, cpu_info->id);
	if (idle_time < 0) {
		if (idle_time != -ENODEV)
			warn("unable to parse idle time for cpu%d\n", cpu_info->id);
		return 0;
	}

	/*
	 * if it is different, there was a change, it does not matter
	 * if it wrapped around.
	 */
	if (cpu_info->idle_time == idle_time)
		return 0;

	if (config_verbose)
		log_msg("last idle time: %u curr idle time:%d ", cpu_info->idle_time, idle_time);

	/*
	 * the CPU had idle time!
	 */
	cpu_info->idle_time = idle_time;

	return 1;
}

int get_cpu_busy_list(struct cpu_info *cpus, int nr_cpus, char *busy_cpu_list)
{
	char sched_stat[STAT_MAX_SIZE];
	struct cpu_info *cpu;
	int busy_count = 0;
	long idle_time;
	int i;

	if (!read_sched_stat(sched_stat, STAT_MAX_SIZE)) {
		warn("fail reading sched stat file");
		warn("disabling idle detection");
		config_idle_detection = 0;

		/* assume they are all busy */
		return nr_cpus;
	}

	for (i = 0; i < nr_cpus; i++) {
		cpu = &cpus[i];
		/*
		 * Consider idle a CPU that has its own monitor.
		 */
		if (cpu->thread_running) {
			if (config_verbose)
				log_msg("\t cpu %d has its own monitor, considering idle\n", cpu->id);
			continue;
		}

		idle_time = get_cpu_idle_time(sched_stat, STAT_MAX_SIZE, cpu->id);
		if (idle_time < 0) {
			if (idle_time != -ENODEV)
				warn("unable to parse idle time for cpu%d\n", cpu->id);
			continue;
		}

		if (config_verbose)
			log_msg ("\t cpu %d had %ld idle time, and now has %ld\n", cpu->id, cpu->idle_time, idle_time);
		/*
		 * if the idle time did not change, the CPU is busy.
		 */
		if (cpu->idle_time == idle_time) {
			busy_cpu_list[i] = 1;
			busy_count++;
			continue;
		}

		cpu->idle_time = idle_time;
	}

	return busy_count;
}
/*
 * read the contents of sched_debug into the input buffer.
 */
int read_sched_debug(char *buffer, int size)
{
	int position = 0;
	int retval;
	int fd;

	fd = open(config_sched_debug_path, O_RDONLY);

	if (!fd)
		goto out_error;

	do {
		retval = read(fd, &buffer[position], size - position);
		if (retval < 0)
			goto out_close_fd;

		position += retval;

	} while (retval > 0 && position < size);

	buffer[position-1] = '\0';

	if (position + 100 > config_buffer_size) {
		config_buffer_size = config_buffer_size * 2;
		log_msg("sched_debug is getting larger, increasing the buffer to %zu\n", config_buffer_size);
	}

	close(fd);

	return position;

out_close_fd:
	close(fd);

out_error:
	return 0;
}

/*
 * find the start of a cpu information block in the
 * input buffer
 */
char *get_cpu_info_start(char *buffer, int cpu)
{
	/* 'cpu#9999,\0' */
	char cpu_header[10];

	sprintf(cpu_header, "cpu#%d,", cpu);

	return strstr(buffer, cpu_header);
}

char *get_next_cpu_info_start(char *start)
{
	const char *next_cpu = "cpu#";

	/* Skipp the current cpu definition */
	start += 10;

	return strstr(start, next_cpu);
}

char *alloc_and_fill_cpu_buffer(int cpu, char *sched_dbg, int sched_dbg_size)
{
	char *next_cpu_start;
	char *cpu_buffer;
	char *cpu_start;
	int size = 0;

	cpu_start = get_cpu_info_start(sched_dbg, cpu);

	/*
	 * The CPU might be offline.
	 */
	if (!cpu_start)
		return NULL;

	next_cpu_start = get_next_cpu_info_start(cpu_start);

	/*
	 * If it did not find the next CPU, it should be the
	 * end of the file.
	 */
	if (!next_cpu_start)
		next_cpu_start = sched_dbg + sched_dbg_size;

	size = next_cpu_start - cpu_start;

	if (size <= 0)
		return NULL;

	cpu_buffer = malloc(size);

	if (!cpu_buffer)
		return NULL;

	strncpy(cpu_buffer, cpu_start, size);

	cpu_buffer[size-1] = '\0';

	return cpu_buffer;
}

/*
 * parsing helpers for skipping whitespace and chars and
 * detecting next line.
 */
static inline char *skipchars(char *str)
{
	while (*str && !isspace(*str))
		str++;
	return str;
}

static inline char *skipspaces(char *str)
{
	while (*str && isspace(*str))
		str++;
	return str;
}

static inline char *nextline(char *str)
{
	char *ptr = strchr(str, '\n');
	return ptr ? ptr+1 : NULL;
}

#define OLD_TASK_FORMAT  1
#define NEW_TASK_FORMAT  2
#define TASK_MARKER	"runnable tasks:"

/*
 * read sched_debug and figure out if it's old or new format
 * done once so if we fail just exit the program
 *
 * NOTE: A side effect of this call is to set the initial value for
 * config_buffer_size used when reading sched_debug for
 * parsing
 */
int detect_task_format(void)
{
	int bufincrement;
	int retval = -1;
	size_t bufsiz;
	char *buffer;
	int size = 0;
	char *ptr;
	int status;
	int fd;

	bufsiz = bufincrement = BUFFER_PAGES * page_size;

	buffer = malloc(bufsiz);

	if (buffer == NULL)
		die("unable to allocate %d bytes to read sched_debug");

	if ((fd = open(config_sched_debug_path, O_RDONLY)) < 0)
		die("error opening sched_debug for reading: %s\n", strerror(errno));

	ptr = buffer;
	while ((status = read(fd, ptr, bufincrement))) {
		if (status < 0)
			die ("error reading sched_debug: %s\n", strerror(errno));
		if (status == 0)
			break;
		size += status;
		bufsiz += bufincrement;
		if ((buffer = realloc(buffer, bufsiz)) == NULL)
			die("realloc failed for %zu size: %s\n", bufsiz, strerror(errno));
		ptr = buffer + size;
	}

	close(fd);

	buffer[size] = '\0';
	config_buffer_size = bufsiz;
	log_msg("initial config_buffer_size set to %zu\n", config_buffer_size);

	ptr = strstr(buffer, TASK_MARKER);
	if (ptr == NULL) {
		fprintf(stderr, "unable to find 'runnable tasks' in buffer, invalid input\n");
		exit(-1);
	}

	ptr += strlen(TASK_MARKER) + 1;
	ptr = skipspaces(ptr);

	if (strncmp(ptr, "task", 4) == 0) {
		retval = OLD_TASK_FORMAT;
		log_msg("detected old task format\n");
	} else if (strncmp(ptr, "S", 1) == 0) {
		retval = NEW_TASK_FORMAT;
		log_msg("detected new task format\n");
	}

	free(buffer);
	return retval;
}


/*
 * Example:
 * ' S           task   PID         tree-key  switches  prio     wait-time             sum-exec        sum-sleep'
 * '-----------------------------------------------------------------------------------------------------------'
 * ' I         rcu_gp     3        13.973264         2   100         0.000000         0.004469         0.000000 0 0 /
 */
int parse_new_task_format(char *buffer, struct task_info *task_info, int nr_entries)
{
	struct task_info *task;
	char *start = buffer;
	int tasks = 0;
	int comm_size;
	char *end;

	/*
	 * if we have less than two tasks on the cpu
	 * there is no possibility of a stall
	 */
	if (nr_entries < 2)
		return 0;

	while (tasks < nr_entries) {
		task = &task_info[tasks];

		/*
		 * only care about tasks in the Runnable state
		 * Note: the actual scheduled task will show up as
		 * "\n>R" so we will skip it.
		 *
		 */
		start = strstr(start, "\n R");

		/*
		 * if no match then there are no more Runnable tasks
		 */
		if (!start)
			break;

		/*
		 * Skip '\n R'
		 */
		start = &start[3];

		/*
		 * skip the spaces.
		 */
		start = skipspaces(start);

		/*
		 * find the end of the string
		 */
		end = skipchars(start);

		comm_size = end - start;

		if (comm_size > COMM_SIZE) {
			warn("comm_size is too large: %d\n", comm_size);
			comm_size = COMM_SIZE;
		}

		strncpy(task->comm, start, comm_size);

		task->comm[comm_size] = '\0';

		/*
		 * go to the end of the task comm
		 */
		start=end;

		task->pid = strtol(start, &end, 10);

		/* get the id of the thread group leader */
		task->tgid = get_tgid(task->pid);

		/*
		 * go to the end of the pid
		 */
		start=end;

		/*
		 * skip the tree-key
		 */
		start = skipspaces(start);
		start = skipchars(start);

		task->ctxsw = strtol(start, &end, 10);

		start = end;

		task->prio = strtol(start, &end, 10);

		task->since = time(NULL);

		/*
		 * go to the end and try to find the next occurence.
		 */
		start = end;

		tasks++;
	}

	return tasks;
}

/*
 * old format of sched_debug doesn't contain state information so we have
 * to pick up the pid and then open /proc/<pid>/stat to get the process state.
 */

static int is_runnable(int pid)
{
	char stat_path[128], stat[512];
	int fd, retval, runnable = 0;
	char *ptr;

	if (pid == 0)
		return 0;
	retval = snprintf(stat_path, sizeof(stat_path), "/proc/%d/stat", pid);
	if (retval < 0 || retval > sizeof(stat_path)) {
		warn("stat path for task %d too long\n", pid);
		goto out_error;
	}
	fd = open(stat_path, O_RDONLY);
	if (!fd) {
		warn("error opening stat path for task %d\n", pid);
		goto out_error;
	}
	flock(fd, LOCK_SH);
	retval = read(fd, &stat, sizeof(stat));
	if (retval < 0) {
		warn("error reading stat for task %d\n", pid);
		goto out_close_fd;
	}
	if (retval < sizeof(stat))
		stat[retval] = '\0';

	/*
	 * the process state is the third white-space delimited
	 * field in /proc/PID/stat. Skip to there and check what
	 * the value is.
	 */
	ptr = skipchars(stat); // skip first word
	ptr = skipspaces(ptr); // skip spaces
	ptr = skipchars(ptr);  // skip second word
	ptr = skipspaces(ptr); // skip spaces

	switch(*ptr) {
	case 'R':
		runnable = 1;
		break;
	case 'S':
	case 'D':
	case 'Z':
 	case 'T':
		break;
	default:
		warn("invalid state(%c) in %s\n", *ptr, stat_path);
	}

out_close_fd:
	flock(fd, LOCK_UN);
	close(fd);
out_error:
	return runnable;
}

static int count_task_lines(char *buffer)
{
	int lines = 0;
	char *ptr;
	int len;

	len = strlen(buffer);

	/* find the runnable tasks: header */
	ptr = strstr(buffer, TASK_MARKER);
	if (ptr == NULL)
		return 0;

	/* skip to the end of the dashed line separator */
	ptr = strstr(ptr, "-\n");
	if (ptr == NULL)
		return 0;

	ptr += 2;
	while(*ptr && ptr < (buffer+len)) {
		lines++;
		ptr = strchr(ptr, '\n');
		if (ptr == NULL)
			break;
		ptr++;
	}
	return lines;
}

/*
 * Example (old format):
 * '            task   PID         tree-key  switches  prio     wait-time             sum-exec        sum-sleep
 * ' ----------------------------------------------------------------------------------------------------------
 * '     watchdog/35   296       -11.731402      4081     0         0.000000        44.052473         0.000000 /
 */
int parse_old_task_format(char *buffer, struct task_info *task_info, int nr_entries)
{
	int pid, ctxsw, prio, comm_size;
	char *start, *end, *buffer_end;
	struct task_info *task;
	char comm[COMM_SIZE+1];
	int waiting_tasks = 0;

	start = buffer;
	start = strstr(start, TASK_MARKER);
	start = strstr(start, "-\n");
	start++;

	buffer_end = buffer + strlen(buffer);

	/*
	 * we can't short-circuit using nr_entries, we have to scan the
	 * entire list of processes that is on this cpu
	 */
	while (*start && start < buffer_end) {
		task = &task_info[waiting_tasks];
		/*
		 * only care about tasks that are not R (running on a CPU).
		 */
		if (start[0] == 'R') {
			/*
			 * Go to the end of the line and ignore this
			 * task.
			 */
			start = strchr(start, '\n');
			start++;
			continue;
		}
		/*
		 * pick up the comm field
		 */
		start = skipspaces(start);
		end = skipchars(start);
		comm_size = end - start;
		if (comm_size > COMM_SIZE) {
			warn("comm_size is too large: %d\n", comm_size);
			comm_size = COMM_SIZE;
		}
		strncpy(comm, start, comm_size);
		comm[comm_size] = 0;
		/*
		 * go to the end of the task comm
		 */
		start=end;
		/*
		 * now pick up the pid
		 */
		pid = strtol(start, &end, 10);
		/*
		 * go to the end of the pid
		 */
		start=end;
		/*
		 * skip the tree-key
		 */
		start = skipspaces(start);
		start = skipchars(start);
		/*
		 * pick up the context switch count
		 */
		ctxsw = strtol(start, &end, 10);
		start = end;
		/*
		 * get the priority
		 */
		prio = strtol(start, &end, 10);
		if (is_runnable(pid)) {
			strncpy(task->comm, comm, comm_size);
			task->comm[comm_size] = 0;
			task->pid = pid;
			task->tgid = get_tgid(task->pid);
			task->ctxsw = ctxsw;
			task->prio = prio;
			task->since = time(NULL);
			waiting_tasks++;
		}
		if ((start = nextline(start)) == NULL)
			break;
		if (waiting_tasks >= nr_entries) {
			break;
		}
	}

	return waiting_tasks;
}


int fill_waiting_task(char *buffer, struct cpu_info *cpu_info)
{
	int nr_waiting = -1;
	int nr_entries;

	if (cpu_info == NULL) {
		warn("NULL cpu_info pointer!\n");
		return 0;
	}
	nr_entries = cpu_info->nr_running;

	switch (config_task_format) {
	case NEW_TASK_FORMAT:
		cpu_info->starving = malloc(sizeof(struct task_info) * nr_entries);
		if (cpu_info->starving == NULL) {
			warn("failed to malloc %d task_info structs", nr_entries);
			return 0;
		}
		nr_waiting = parse_new_task_format(buffer, cpu_info->starving, nr_entries);
		break;
	case OLD_TASK_FORMAT:
		/*
		 * the old task format does not output a correct value for nr_running
		 * (the initializer for nr_entries) so count the task lines for this cpu
		 * data and use that instead
		 */
		nr_entries = count_task_lines(buffer);
		if (nr_entries <= 0)
			return 0;
		cpu_info->starving = malloc(sizeof(struct task_info) * nr_entries);
		if (cpu_info->starving == NULL) {
			warn("failed to malloc %d task_info structs", nr_entries);
			return 0;
		}
		nr_waiting = parse_old_task_format(buffer, cpu_info->starving, nr_entries);
		break;
	default:
		die("invalid value for config_task_format: %d\n", config_task_format);
	}
	return nr_waiting;
}

void print_waiting_tasks(struct cpu_info *cpu_info)
{
	time_t now = time(NULL);
	struct task_info *task;
	int i;

	printf("CPU %d has %d waiting tasks\n", cpu_info->id, cpu_info->nr_waiting_tasks);
	if (!cpu_info->nr_waiting_tasks)
		return;

	for (i = 0; i < cpu_info->nr_waiting_tasks; i++) {
		task = &cpu_info->starving[i];

		printf("%15s %9d %9d %9d %9ld\n", task->comm, task->pid, task->prio, task->ctxsw, (now - task->since));
	}

	return;

}

struct cpu_starving_task_info {
	struct task_info task;
	int pid;
	time_t since;
	int overloaded;
};

struct cpu_starving_task_info *cpu_starving_vector;

void update_cpu_starving_vector(int cpu, int pid, time_t since, struct task_info *task)
{
	struct cpu_starving_task_info *cpu_info = &cpu_starving_vector[cpu];

	/*
	 * If there is another thread already here, mark this cpu as
	 * overloaded.
	 */
	if (cpu_info->pid)
		cpu_info->overloaded = 1;

	/*
	 * If there is no thread in the vector, or if the in the
	 * vector has an earlier since (time stamp), update it.
	 */
	if ((cpu_info->since == 0) || cpu_info->since > since) {
		memcpy(&(cpu_info->task), task, sizeof(struct task_info));
		cpu_info->pid = pid;
		cpu_info->since = since;
	}
}

void merge_taks_info(int cpu, struct task_info *old_tasks, int nr_old, struct task_info *new_tasks, int nr_new)
{
	struct task_info *old_task;
	struct task_info *new_task;
	int i;
	int j;

	for (i = 0; i < nr_old; i++) {
		old_task = &old_tasks[i];

		for (j = 0; j < nr_new; j++) {
			new_task = &new_tasks[j];

			if (old_task->pid == new_task->pid) {
				if (old_task->ctxsw == new_task->ctxsw) {
					new_task->since = old_task->since;
					if (config_single_threaded)
						update_cpu_starving_vector(cpu, new_task->pid, new_task->since, new_task);
				}
				break;
			}
		}
	}
}

int parse_cpu_info(struct cpu_info *cpu_info, char *buffer, size_t buffer_size)
{

	struct task_info *old_tasks = cpu_info->starving;
	int nr_old_tasks = cpu_info->nr_waiting_tasks;
	long nr_running = 0, nr_rt_running = 0;
	int cpu = cpu_info->id;
	char *cpu_buffer;
	int retval = 0;

	cpu_buffer = alloc_and_fill_cpu_buffer(cpu, buffer, buffer_size);
	/*
	 * It is not necessarily a problem, the CPU might be offline. Cleanup
	 * and leave.
	 */
	if (!cpu_buffer) {
		if (old_tasks)
			free(old_tasks);
		cpu_info->nr_waiting_tasks = 0;
		cpu_info->nr_running = 0;
		cpu_info->nr_rt_running = 0;
		cpu_info->starving = 0;
		goto out;
	}

	/*
	 * The NEW_TASK_FORMAT produces useful output values for nr_running and
	 * rt_nr_running, so in this case use them. For the old format just leave
	 * them initialized to zero.
	 */
	if (config_task_format == NEW_TASK_FORMAT) {
		nr_running = get_variable_long_value(cpu_buffer, ".nr_running");
		nr_rt_running = get_variable_long_value(cpu_buffer, ".rt_nr_running");
		if ((nr_running == -1) || (nr_rt_running == -1)) {
			retval = -EINVAL;
			goto out_free;
		}
	}

	cpu_info->nr_running = nr_running;
	cpu_info->nr_rt_running = nr_rt_running;

	cpu_info->nr_waiting_tasks = fill_waiting_task(cpu_buffer, cpu_info);
	if (old_tasks) {
		merge_taks_info(cpu_info->id, old_tasks, nr_old_tasks, cpu_info->starving, cpu_info->nr_waiting_tasks);
		free(old_tasks);
	}

out_free:
	free(cpu_buffer);
out:
	return retval;
}

int get_current_policy(int pid, struct sched_attr *attr)
{
	int ret;

	ret = sched_getattr(pid, attr, sizeof(*attr), 0);
	if (ret == -1)
		log_msg("get_current_policy: failed with error %s\n", strerror(errno));
	return ret;
}

int boost_with_deadline(int pid)
{
	struct sched_attr attr;
	int flags = 0;
	int ret;

	memset(&attr, 0, sizeof(attr));
	attr.size = sizeof(attr);
	attr.sched_policy   = SCHED_DEADLINE;
	attr.sched_runtime  = config_dl_runtime;
	attr.sched_deadline = config_dl_period;
	attr.sched_period   = config_dl_period;

	ret = sched_setattr(pid, &attr, flags);
	if (ret < 0) {
	    log_msg("boost_with_deadline failed to boost pid %d: %s\n", pid, strerror(errno));
	    return ret;
	}

	log_msg("boosted pid %d using SCHED_DEADLINE\n", pid);
	return ret;
}

int boost_with_fifo(int pid)
{
	struct sched_attr attr;
	int flags = 0;
	int ret;

	memset(&attr, 0, sizeof(attr));
	attr.size = sizeof(attr);
	attr.sched_policy   = SCHED_FIFO;
	attr.sched_priority = config_fifo_priority;

	ret = sched_setattr(pid, &attr, flags);
	if (ret < 0) {
	    log_msg("boost_with_fifo failed to boost pid %d: %s\n", pid, strerror(errno));
	    return ret;
	}
	log_msg("boosted pid %d using SCHED_FIFO\n", pid);
	return ret;
}

int restore_policy(int pid, struct sched_attr *attr)
{
	int flags = 0;
	int ret;

	ret = sched_setattr(pid, attr, flags);
	if (ret < 0)
		log_msg("restore_policy: failed to restore sched policy for pid %d: %s\n",
			pid, strerror(errno));
	return ret;
}

/*
 * this function emulates the behavior of SCHED_DEADLINE but
 * using SCHED_FIFO by boosting the thread, sleeping for runtime,
 * changing the pid policy back to its old policy, then sleeping
 * for the remainder of the period, repeating until all the
 * periods are done.
 */
void do_fifo_boost(int pid, struct sched_attr *old_attr)
{
	int nr_periods = config_boost_duration / config_dl_period;
	struct timespec remainder_ts;
	struct timespec runtime_ts;
	struct timespec ts;
	int i;

	/*
	 * setup the runtime sleep
	 */
	memset(&runtime_ts, 0, sizeof(runtime_ts));
	runtime_ts.tv_nsec = config_dl_runtime;
	normalize_timespec(&runtime_ts);

	/*
	 * setup the remainder of the period sleep
	 */
	memset(&remainder_ts, 0, sizeof(remainder_ts));
	remainder_ts.tv_nsec = config_dl_period - config_dl_runtime;
	normalize_timespec(&remainder_ts);

	for (i=0; i < nr_periods; i++) {
		boost_with_fifo(pid);
		ts = runtime_ts;
		clock_nanosleep(CLOCK_MONOTONIC, 0, &ts, 0);
		restore_policy(pid, old_attr);
		ts = remainder_ts;
		clock_nanosleep(CLOCK_MONOTONIC, 0, &ts, 0);
	}
}

int boost_starving_task(int pid)
{
	struct sched_attr attr;
	int ret;

	/*
	 * Get the old prio, to be restored at the end of the
	 * boosting period.
	 */
	ret = get_current_policy(pid, &attr);
	if (ret < 0)
		return ret;

	/*
	 * Boost.
	 */
	if (boost_policy == SCHED_DEADLINE) {
		ret = boost_with_deadline(pid);
		if (ret < 0)
			return ret;
		sleep(config_boost_duration);
		ret = restore_policy(pid, &attr);
		if (ret < 0)
			return ret;
	} else {
		do_fifo_boost(pid, &attr);
	}

	/*
	 * XXX: If the proccess dies, we get an error. Deal with that
	 * latter.
	 * if (ret < 0)
	 *   die("sched_setattr failed to set the normal priorities");
	 */

	return 0;

}

/*
 * API to check if the task must not be considered
 * for priority boosting. The task's name itself will
 * be checked or the name of the task group it is a
 * part of will be checked
 */
int check_task_ignore(struct task_info *task) {
	char *group_comm = NULL;
	int ret = -EINVAL;
	unsigned int i;

	/*
	 * check if this task's name has been passed as part of the
	 * thread ignore regex
	 */
	for (i = 0; i < nr_thread_ignore; i++) {
		ret = regexec(&compiled_regex_thread[i], task->comm, REGEXEC_NO_NMATCH,
				REGEXEC_NO_MATCHPTR, REGEXEC_NO_FLAGS);
		if (!ret) {
			log_msg("Ignoring the thread %s from consideration for boosting\n", task->comm);
			return ret;
		}
	}
	ret = -EINVAL;

	/*
	 * if a valid tgid has been found and its not that of the
	 * swapper (because its not listed on the /proc filesystem)
	 * then proceed to fetch the name of the process
	 */
	if (task->tgid > SWAPPER) {
		group_comm = get_process_comm(task->tgid);
		if (group_comm == NULL) {
			warn("Ran into a tgid without process name");
			return ret;
		}
		/*
		 * check if the process group that this task is a part has been
		 * requested to be ignored
		 */
		for (i = 0; i < nr_process_ignore; i++) {
			ret = regexec(&compiled_regex_process[i], group_comm, REGEXEC_NO_NMATCH,
					REGEXEC_NO_MATCHPTR, REGEXEC_NO_FLAGS);
			if (!ret) {
				log_msg("Ignoring the thread %s (spawned by %s) from consideration for boosting\n", task->comm, group_comm);
				goto free_mem;
			}
		}
	}
free_mem:
	if (group_comm != NULL)
		free(group_comm);
	return ret;
}

int check_starving_tasks(struct cpu_info *cpu)
{
	struct task_info *tasks = cpu->starving;
	struct task_info *task;
	int starving = 0;
	int i;

	for (i = 0; i < cpu->nr_waiting_tasks; i++) {
		task = &tasks[i];

		if ((time(NULL) - task->since) >= config_starving_threshold) {

			log_msg("%s-%d starved on CPU %d for %d seconds\n",
				task->comm, task->pid, cpu->id,
				(time(NULL) - task->since));

			/* check if this task needs to be ignored from being boosted
			 * if yes, update the time stamp so that it doesn't keep
			 * getting reported as being starved
			 */
			if (config_ignore && !(check_task_ignore(task))) {
				task->since = time(NULL);
				continue;
			}

			starving+=1;

			/*
			 * It it is only logging, just reset the time couter
			 * after logging.
			 */
			if (config_log_only) {
				task->since = time(NULL);
				continue;
			}

			boost_starving_task(task->pid);
		}
	}

	return starving;
}

int check_might_starve_tasks(struct cpu_info *cpu)
{
	struct task_info *tasks = cpu->starving;
	struct task_info *task;
	int starving = 0;
	int i;

	if (cpu->thread_running)
		warn("checking a running thread!!!???");

	for (i = 0; i < cpu->nr_waiting_tasks; i++) {
		task = &tasks[i];

		if ((time(NULL) - task->since) >= config_starving_threshold/2) {

			log_msg("%s-%d might starve on CPU %d (waiting for %d seconds)\n",
				task->comm, task->pid, cpu->id,
				(time(NULL) - task->since));

			starving = 1;
		}
	}

	return starving;
}

void *cpu_main(void *data)
{
	struct cpu_info *cpu = data;
	int nothing_to_do = 0;
	int retval;

	while (cpu->thread_running && running) {

		/*
		 * Buffer size should increase. See read_sched_debug().
		 */
		if (config_buffer_size != cpu->buffer_size) {
			char *old_buffer = cpu->buffer;
			cpu->buffer = realloc(cpu->buffer, config_buffer_size);
			if (!cpu->buffer) {
				warn("fail to increase the buffer... continue");
				cpu->buffer = old_buffer;
			} else {
				cpu->buffer_size = config_buffer_size;
			}
		}

		if (config_idle_detection) {
			if (cpu_had_idle_time(cpu)) {
				if (config_verbose)
					log_msg("cpu %d had idle time! skipping next phase\n", cpu->id);
				nothing_to_do++;
				goto skipped;
			}
		}

		retval = read_sched_debug(cpu->buffer, cpu->buffer_size);
		if(!retval) {
			warn("fail reading sched debug file");
			warn("Dazed and confused, but trying to continue");
			continue;
		}

		retval = parse_cpu_info(cpu, cpu->buffer, cpu->buffer_size);
		if (retval) {
			warn("error parsing CPU info");
			warn("Dazed and confused, but trying to continue");
			continue;
		}

		if (config_verbose)
			print_waiting_tasks(cpu);

		if ((config_task_format == NEW_TASK_FORMAT && cpu->nr_rt_running) ||
		    cpu->nr_waiting_tasks) {
			nothing_to_do = 0;
			check_starving_tasks(cpu);
		} else {
			nothing_to_do++;
		}

skipped:
		/*
		 * it not in aggressive mode, give up after 10 cycles with
		 * nothing to do.
		 */
		if (!config_aggressive && nothing_to_do == 10) {
			cpu->thread_running=0;
			pthread_exit(NULL);
		}

		sleep(config_granularity);
	}

	return NULL;
}

static const char *join_thread(pthread_t *thread)
{
	void *result;

	pthread_join(*thread, &result);

	return result;
}

void aggressive_main(struct cpu_info *cpus, int nr_cpus)
{
	int i;

	for (i = 0; i < nr_cpus; i++) {
		if (!should_monitor(i))
			continue;

		cpus[i].id = i;
		cpus[i].thread_running = 1;
		pthread_create(&cpus[i].thread, NULL, cpu_main, &cpus[i]);
	}

	for (i = 0; i < nr_cpus; i++) {
		if (!should_monitor(i))
			continue;

		join_thread(&cpus[i].thread);
	}
}

void conservative_main(struct cpu_info *cpus, int nr_cpus)
{
	char busy_cpu_list[nr_cpus];
	pthread_attr_t dettached;
	size_t buffer_size = 0;
	struct cpu_info *cpu;
	char *buffer = NULL;
	int has_busy_cpu;
	int retval;
	int i;

	buffer = malloc(config_buffer_size);
	if (!buffer)
		die("cannot allocate buffer");

	buffer_size = config_buffer_size;

	pthread_attr_init(&dettached);
	pthread_attr_setdetachstate(&dettached, PTHREAD_CREATE_DETACHED);

	for (i = 0; i < nr_cpus; i++) {
		cpus[i].id = i;
		cpus[i].thread_running = 0;
	}

	while (running) {

		/*
		 * Buffer size should increase. See read_sched_debug().
		 */
		if (config_buffer_size != buffer_size) {
			char *old_buffer = buffer;
			buffer = realloc(buffer, config_buffer_size);
			if (!buffer) {
				warn("fail to increase the buffer... continue");
				buffer = old_buffer;
			} else {
				buffer_size = config_buffer_size;
			}
		}

		if (config_idle_detection) {
			memset(&busy_cpu_list, 0, nr_cpus);
			has_busy_cpu = get_cpu_busy_list(cpus, nr_cpus, busy_cpu_list);
			if (!has_busy_cpu) {
				if (config_verbose)
					log_msg("all CPUs had idle time, skipping sched_debug parse\n");
				goto skipped;
			}
		}

		retval = read_sched_debug(buffer, buffer_size);
		if (!retval) {
			warn("Dazed and confused, but trying to continue");
			continue;
		}

		for (i = 0; i < nr_cpus; i++) {
			if (!should_monitor(i))
				continue;

			cpu = &cpus[i];

			if (cpu->thread_running)
				continue;

			if (config_idle_detection && !busy_cpu_list[i])
				continue;

			retval = parse_cpu_info(cpu, buffer, buffer_size);
			if (retval) {
				warn("error parsing CPU info");
				warn("Dazed and confused, but trying to continue");
				continue;
			}

			if (config_verbose)
				printf("\tchecking cpu %d - rt: %d - starving: %d\n",
				       i, cpu->nr_rt_running, cpu->nr_waiting_tasks);

			if (check_might_starve_tasks(cpu)) {
				cpus[i].id = i;
				cpus[i].thread_running = 1;
				pthread_create(&cpus[i].thread, &dettached, cpu_main, &cpus[i]);
			}
		}

skipped:
		sleep(config_granularity);
	}
}

int boost_cpu_starving_vector(struct cpu_starving_task_info *vector, int nr_cpus)
{
	struct cpu_starving_task_info *cpu;
	struct sched_attr attr[nr_cpus];
	int deboost_vector[nr_cpus];
	int boosted = 0;
	time_t now;
	int ret;
	int i;

	now = time(NULL);

	/*
	 * Boost phase.
	 */
	for (i = 0; i < nr_cpus; i++) {

		/*
		 * clear the deboost vector for this CPU.
		 */
		deboost_vector[i] = 0;

		cpu = &cpu_starving_vector[i];

		if (config_verbose && cpu->pid)
			log_msg("\t cpu %d: pid: %d starving for %llu\n", i, cpu->pid, (now - cpu->since));

		if (config_log_only)
			continue;

		if (cpu->pid != 0 && (now - cpu->since) > config_starving_threshold) {
			/*
			 * Check if this task name is part of a denylist
			 * If yes, do not boost it
			 */
			if (config_ignore && !check_task_ignore(&cpu->task))
				continue;

			/*
			 * Save the task policy.
			 */
			ret = get_current_policy(cpu->pid, &attr[i]);
			/*
			 * It is ok if a task die.
			 */
			if (ret < 0)
				continue;

			/*
			 * Boost!
			 */
			ret = boost_with_deadline(cpu->pid);
			/*
			 * It is ok if a task die.
			 */
			if (ret < 0)
				continue;

			/*
			 * Save it for the deboost.
			 */
			deboost_vector[i] = cpu->pid;

			boosted++;
		}
	}

	if (!boosted)
		return 0;

	sleep(config_boost_duration);

	for (i = 0; i < nr_cpus; i++) {
		if (deboost_vector[i] != 0)
			restore_policy(deboost_vector[i], &attr[i]);
	}

	return boosted;
}

void single_threaded_main(struct cpu_info *cpus, int nr_cpus)
{
	char busy_cpu_list[nr_cpus];
	size_t buffer_size = 0;
	struct cpu_info *cpu;
	char *buffer = NULL;
	int overloaded = 0;
	int has_busy_cpu;
	int boosted = 0;
	int retval;
	int i;

	log_msg("single threaded mode\n");

	if (!config_log_only && boost_policy != SCHED_DEADLINE)
		die("Single threaded mode only works with SCHED_DEADLINE");

	cpu_starving_vector = malloc(sizeof(struct cpu_starving_task_info) * nr_cpus);
	if (!cpu_starving_vector)
		die("cannot allocate cpu starving vector");

	buffer = malloc(config_buffer_size);
	if (!buffer)
		die("cannot allocate buffer");

	buffer_size = config_buffer_size;

	for (i = 0; i < nr_cpus; i++) {
		cpus[i].id = i;
		cpus[i].thread_running = 0;
		cpu_starving_vector[i].pid = 0;
		cpu_starving_vector[i].since = 0;
		cpu_starving_vector[i].overloaded = 0;
		memset(&cpu_starving_vector[i].task, 0, sizeof(struct task_info));
	}

	while (running) {

		/*
		 * Buffer size should increase. See read_sched_debug().
		 */
		if (config_buffer_size != buffer_size) {
			char *old_buffer = buffer;
			buffer = realloc(buffer, config_buffer_size);
			if (!buffer) {
				warn("fail to increase the buffer... continue");
				buffer = old_buffer;
			} else {
				buffer_size = config_buffer_size;
			}
		}

		if (config_idle_detection) {
			memset(&busy_cpu_list, 0, nr_cpus);
			has_busy_cpu = get_cpu_busy_list(cpus, nr_cpus, busy_cpu_list);
			if (!has_busy_cpu) {
				if (config_verbose)
					log_msg("all CPUs had idle time, skipping sched_debug parse\n");

				goto skipped;
			}
		}

		retval = read_sched_debug(buffer, buffer_size);
		if (!retval) {
			warn("Dazed and confused, but trying to continue");
			continue;
		}

		for (i = 0; i < nr_cpus; i++) {
			if (!should_monitor(i))
				continue;

			cpu = &cpus[i];

			if (config_idle_detection && !busy_cpu_list[i])
				continue;

			retval = parse_cpu_info(cpu, buffer, buffer_size);
			if (retval) {
				warn("error parsing CPU info");
				warn("Dazed and confused, but trying to continue");
				continue;
			}

			if (config_verbose)
				printf("\tchecking cpu %d - rt: %d - starving: %d\n",
				       i, cpu->nr_rt_running, cpu->nr_waiting_tasks);

		}

		boosted = boost_cpu_starving_vector(cpu_starving_vector, nr_cpus);
		if (!boosted)
			goto skipped;

		/* Cleanup the cpu starving vector */
		for (i = 0; i < nr_cpus; i++) {
			memset(&(cpu_starving_vector[i].task), 0, sizeof(struct task_info));
			cpu_starving_vector[i].pid = 0;
			cpu_starving_vector[i].since = 0;
			if (cpu_starving_vector[i].overloaded)
				overloaded = 1;
			cpu_starving_vector[i].overloaded = 0;
		}

		/*
		 * If any CPU had more than one thread starving, the system is overloaded.
		 * Re-run the loop without sleeping for two reasons: to boost the other
		 * thread, and to detect other starving threads on other CPUs, given
		 * that the system seems to be overloaded.
		 */
		if (overloaded) {
			overloaded = 0;
			continue;
		}

skipped:
		/*
		 * if no boost was required, just sleep.
		 */
		if (!boosted) {
			sleep(config_granularity);
			continue;
		}

		/*
		 * if the boost duration is longer than the granularity, there
		 * is no need for a sleep.
		 */
		if (config_granularity <= config_boost_duration)
			continue;

		/*
		 * Ok, sleep for the rest of the time.
		 *
		 * (yeah, but is it worth to get the time to compute the overhead?
		 * at the end, it should be less than one second anyway.)
		 */
		sleep(config_granularity - config_boost_duration);
	}
	if (buffer)
		free(buffer);
}


int check_policies(void)
{
	int saved_runtime = config_dl_runtime;
	int boosted = SCHED_DEADLINE;
	struct sched_attr attr;
	int ret;

	/*
	 * if we specified fifo on the command line
	 * just return false
	 */
	if (config_force_fifo) {
		log_msg("forcing SCHED_FIFO for boosting\n");
		return SCHED_FIFO;
	}

	// set runtime to half of period
	config_dl_runtime = config_dl_period / 2;

	// save off the current policy
	if (get_current_policy(0, &attr))
		die("unable to get scheduling policy!");

	// try boosting to SCHED_DEADLINE
	ret = boost_with_deadline(0);
	if (ret < 0) {
		// try boosting with fifo to see if we have permission
		ret = boost_with_fifo(0);
		if (ret < 0) {
			log_msg("check_policies: unable to change policy to either deadline or fifo,"
				"defaulting to logging only\n");
			config_log_only = 1;
			boosted = 0;
		}
		else
			boosted = SCHED_FIFO;
	}
	// if we successfully boosted to something, restore the old policy
	if (boosted) {
		ret = restore_policy(0, &attr);
		// if we can't restore the policy then quit now
		if (ret < 0)
			die("unable to restore policy: %s\n", strerror(errno));
 	}

	// restore the actual runtime value
	config_dl_runtime = saved_runtime;
	if (boosted == SCHED_DEADLINE)
		log_msg("using SCHED_DEADLINE for boosting\n");
	else if (boosted == SCHED_FIFO)
		log_msg("using SCHED_FIFO for boosting\n");
	return boosted;
}

int main(int argc, char **argv)
{
	struct cpu_info *cpus;
	int nr_cpus;
	int i;

	/*
	 * get the system page size so we can use it
	 * when allocating buffers
	 */
	if ((page_size = sysconf(_SC_PAGE_SIZE)) < 0)
		die("Unable to get system page size: %s\n", strerror(errno));

	parse_args(argc, argv);

	find_sched_debug_path();

	/*
	 * check RT throttling
	 * if --systemd was specified then RT throttling should already be off
	 * otherwise turn it off
	 * in both cases verify that it actually got turned off since we can't
	 * run with it on.
	 */
	if (config_systemd) {
		if (!config_log_only && !rt_throttling_is_off())
			die ("RT throttling is on! stalld cannot run...\n");
	}
	else if (!config_log_only) {
		turn_off_rt_throttling();
		if (!rt_throttling_is_off())
			die("turning off RT throttling failed, stalld cannot run\n");
	}

	/*
	 * see if deadline scheduler is available
	 */
	if (!config_log_only)
		boost_policy = check_policies();

	nr_cpus = sysconf(_SC_NPROCESSORS_CONF);
	if (nr_cpus < 1)
		die("Can not calculate number of CPUS\n");

	cpus = malloc(sizeof(struct cpu_info) * nr_cpus);
	if (!cpus)
		die("Cannot allocate memory");

	memset(cpus, 0, sizeof(struct cpu_info) * nr_cpus);

	for (i = 0; i < nr_cpus; i++) {
		cpus[i].buffer = malloc(config_buffer_size);
		if (!cpus[i].buffer)
			die("Cannot allocate memory");

		cpus[i].buffer_size = config_buffer_size;
	}

	if (config_log_syslog)
		openlog("stalld", 0, LOG_DAEMON);

	config_task_format = detect_task_format();

	setup_signal_handling();

	if (config_idle_detection)
		STAT_MAX_SIZE = nr_cpus * page_size;

	if (!config_foreground)
		deamonize();

	write_pidfile();

	/*
	 * The less likely first.
	 */
	if (config_aggressive)
		aggressive_main(cpus, nr_cpus);
	else if (config_adaptive_multi_threaded)
		conservative_main(cpus, nr_cpus);
	else
		single_threaded_main(cpus, nr_cpus);

	cleanup_regex(&nr_thread_ignore, &compiled_regex_thread);
	cleanup_regex(&nr_process_ignore, &compiled_regex_process);
	if (config_log_syslog)
		closelog();

	exit(0);
}
