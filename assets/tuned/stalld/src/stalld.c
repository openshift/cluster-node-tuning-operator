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
long config_starving_threshold = 60;
long config_boost_duration = 3;
long config_aggressive = 0;

/*
 * XXX: Make it a cpu mask, lazy Daniel!
 */
int config_monitor_all_cpus = 1;
char *config_monitored_cpus;

/*
 * Max known to be enough sched_debug buffer size. It increases if the
 * file gets larger.
 */
int config_buffer_size = BUFFER_SIZE;

/*
 * auto-detected task format from /proc/sched_debug
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
* read the contents of /proc/sched_debug into
 * the input buffer
 */
int read_sched_debug(char *buffer, int size)
{
	int position = 0;
	int retval;
	int fd;

	fd = open("/proc/sched_debug", O_RDONLY);

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
		log_msg("sched_debug is getting larger, increasing the buffer to %d\n", config_buffer_size);
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
 * parsing helpers for skipping whitespace and chars
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
 * read /proc/sched_debug and figure out if it's old or new format
 * done once so if we fail just exit the program
 */
int detect_task_format(void)
{
	int bufsiz = BUFFER_SIZE;
	int size = 0;
	int fd;
	char *buffer = malloc(BUFFER_SIZE);
	char *ptr = buffer;
	int retval = -1;
	int status;

	if (buffer == NULL)
		die("detect_task_format: unable to allocate %d bytes to read /proc/sched_debug");

	if ((fd = open("/proc/sched_debug", O_RDONLY)) < 0)
		die("detect_task_format: error opening /proc/sched_debug for reading: %s\n", strerror(errno));

	while ((status = read(fd, ptr, BUFFER_SIZE))) {
		if (status < 0)
			die ("detect_task_format: error reading /proc/sched_debug: %s\n", strerror(errno));
		size += status;
		bufsiz += BUFFER_SIZE;
		if ((buffer = realloc(buffer, bufsiz)) == NULL)
			die("detect_task_format: realloc failed for %d size: %s\n", bufsiz, strerror(errno));
		ptr = buffer + size;
	}
	close(fd);
	buffer[size] = '\0';
	config_buffer_size = bufsiz;
	log_msg("initial config_buffer_size set to %d\n", config_buffer_size);

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
	}
	else if (strncmp(ptr, "S", 1) == 0) {
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
 * old format of /proc/sched_debug doesn't contain state information so we have
 * to pick up the pid and then open /proc/<pid>/stat to get the process state.
 */

static int is_runnable(int pid)
{
	int fd, retval, runnable = 0;
	char stat_path[128], stat[512];
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
		warn("is_runnable: invalid state(%c) in %s\n", *ptr, stat_path);
	}

out_close_fd:
	flock(fd, LOCK_UN);
	close(fd);
out_error:
	return runnable;
}

static int count_task_lines(char *buffer)
{
	char *ptr;
	int len = strlen(buffer);
	int lines = 0;

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
	struct task_info *task;
	char *start = buffer;
	int waiting_tasks = 0;
	int comm_size;
	char *end;
	char *buffer_end = start+strlen(buffer);

	start = strstr(start, TASK_MARKER);
	start = strstr(start, "-\n");
	start++;

	/*
	 * we can't short-circuit using nr_entries, we have to scan the
	 * entire list of processes that is on this cpu
	 */
	while (*start && start < buffer_end) {
		int pid, ctxsw, prio;
		char comm[COMM_SIZE+1];
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


int fill_waiting_task(char *buffer, struct cpu_info *cpu_info, int nr_entries)
{
	int nr_waiting = -1;
	int lines;

	switch (config_task_format) {
	case NEW_TASK_FORMAT:
		cpu_info->starving = malloc(sizeof(struct task_info) * cpu_info->nr_running);
		if (cpu_info->starving == NULL) {
			warn("fill_waiting_task: failed to malloc %d task_info structs", cpu_info->nr_running);
			return 0;
		}
		nr_waiting = parse_new_task_format(buffer, cpu_info->starving, nr_entries);
		break;
	case OLD_TASK_FORMAT:
		/* count the number of tasks listed */
		lines = count_task_lines(buffer);
		if (lines <= 0)
			return 0;
		cpu_info->starving = malloc(sizeof(struct task_info) * lines);
		nr_waiting = parse_old_task_format(buffer, cpu_info->starving, nr_entries);
		break;
	default:
		die("fill_waiting_task: invalid value for config_task_format: %d\n", config_task_format);
	}
	return nr_waiting;
}

void print_waiting_tasks(struct cpu_info *cpu_info)
{
	struct task_info *task;
	time_t now = time(NULL);
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

void merge_taks_info(struct task_info *old_tasks, int nr_old, struct task_info *new_tasks, int nr_new)
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
				if (old_task->ctxsw == new_task->ctxsw)
					new_task->since = old_task->since;

				break;
			}
		}
	}
}

int parse_cpu_info(struct cpu_info *cpu_info, char *buffer, int buffer_size)
{

	struct task_info *old_tasks = cpu_info->starving;
	int nr_old_tasks = cpu_info->nr_waiting_tasks;
	long nr_running, nr_rt_running;
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

	nr_running = get_variable_long_value(cpu_buffer, ".nr_running");
	nr_rt_running = get_variable_long_value(cpu_buffer, ".rt_nr_running");

	if ((nr_running == -1) || (nr_rt_running == -1)) {
		retval = -EINVAL;
		goto out_free;
	}

	cpu_info->nr_running = nr_running;
	cpu_info->nr_rt_running = nr_rt_running;
	cpu_info->nr_waiting_tasks = fill_waiting_task(cpu_buffer, cpu_info, cpu_info->nr_running);
	if (old_tasks) {
		merge_taks_info(old_tasks, nr_old_tasks, cpu_info->starving, cpu_info->nr_waiting_tasks);
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
	int ret;
	int flags = 0;
	struct sched_attr attr;

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
	int ret;
	int flags = 0;
	struct sched_attr attr;

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
	int ret;
	int flags = 0;

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
	int i;
	int nr_periods = config_boost_duration / config_dl_period;
	struct timespec runtime_ts;
	struct timespec remainder_ts;
	struct timespec ts;

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
	int ret;
	struct sched_attr attr;

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
	}
	else
		do_fifo_boost(pid, &attr);

	/*
	 * XXX: If the proccess dies, we get an error. Deal with that
	 * latter.
	 * if (ret < 0)
	 *   die("sched_setattr failed to set the normal priorities");
	 */

	return 0;

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

		if (cpu->nr_rt_running && cpu->nr_waiting_tasks) {
			nothing_to_do = 0;
			check_starving_tasks(cpu);
		} else {
			nothing_to_do++;
		}

		/*
		 * it not in aggressive mode, give up after 10 cycles with
		 * nothing to do.
		 */
		if (!config_aggressive && nothing_to_do == 10) {
			cpu->thread_running=0;
			pthread_exit(NULL);
		}

		sleep(1);
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
	pthread_attr_t dettached;
	struct cpu_info *cpu;
	char *buffer = NULL;
	int buffer_size = 0;
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

		retval = read_sched_debug(buffer, buffer_size);
		if(!retval) {
			warn("Dazed and confused, but trying to continue");
			continue;
		}

		for (i = 0; i < nr_cpus; i++) {
			if (!should_monitor(i))
				continue;

			cpu = &cpus[i];

			if (cpu->thread_running)
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

		sleep(MAX(config_starving_threshold/20,1));
	}
}


int check_policies(void)
{
	int ret;
	int saved_runtime = config_dl_runtime;
	int boosted = SCHED_DEADLINE;
	struct sched_attr attr;

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
		die("check_policies: unable to get scheduling policy!");

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
			die("check_policies: unable to restore policy: %s\n", strerror(errno));
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

	parse_args(argc, argv);

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



	if (!config_foreground)
		deamonize();

	write_pidfile();

	if (config_aggressive)
		aggressive_main(cpus, nr_cpus);
	else
		conservative_main(cpus, nr_cpus);

	if (config_log_syslog)
		closelog();

	exit(0);
}
