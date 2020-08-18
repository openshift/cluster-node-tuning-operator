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

#define BUFFER_SIZE		(1024 * 1000)
#define MAX_WAITING_PIDS	30

/*
 * See kernel/sched/debug.c:print_task().
 */
struct task_info {
	int pid;
	int prio;
	int ctxsw;
	time_t since;
	char comm[15];
};

struct cpu_info {
	int id;
	int nr_running;
	int nr_rt_running;
	int ctxsw;
	int nr_waiting_tasks;
	int thread_running;
	struct task_info *starving;
	pthread_t thread;
};

#ifdef __x86_64__
# define __NR_sched_setattr 314
# define __NR_sched_getattr 315
#elif __i386__
# define __NR_sched_setattr 351
# define __NR_sched_getattr 352
#elif __arm__
# define __NR_sched_setattr 380
# define __NR_sched_getattr 381
#elif __aarch64__
# define __NR_sched_setattr 274
# define __NR_sched_getattr 275
#elif __powerpc__
# define __NR_sched_setattr 355
# define __NR_sched_getattr 356
#elif __s390x__
# define __NR_sched_setattr 345
# define __NR_sched_getattr 346
#endif

struct sched_attr {
	uint32_t size;
	uint32_t sched_policy;
	uint64_t sched_flags;
	int32_t sched_nice;
	uint32_t sched_priority;
	uint64_t sched_runtime;
	uint64_t sched_deadline;
	uint64_t sched_period;
};

int sched_setattr(pid_t pid, const struct sched_attr *attr,
		  unsigned int flags) {
	return syscall(__NR_sched_setattr, pid, attr, flags);
}

int sched_getattr(pid_t pid, struct sched_attr *attr,
		  unsigned int size, unsigned int flags)
{
	return syscall (__NR_sched_getattr, pid , attr, size, flags);
}

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
 * control loop (time in seconds)
 */
long config_starving_threshold = 60;
long config_boost_duration = 3;
long config_aggressive = 0;

#define NS_PER_SEC 1000000000

/*
 * XXX: Make it a cpu mask, lazy Daniel!
 */
int config_monitor_all_cpus = 1;
char *config_monitored_cpus;


/*
 * print any error messages and exit
 */

void die(const char *fmt, ...)
{
	va_list ap;
	int ret = errno;

	if (errno)
		perror("stalld: ");
	else
		ret = -1;

	va_start(ap, fmt);
	fprintf(stderr, "  ");
	vfprintf(stderr, fmt, ap);
	va_end(ap);

	fprintf(stderr, "\n");
	exit(ret);
}

/*
 * path to file for storing daemon pid
 */
char pidfile[MAXPATHLEN];

void log_msg(const char *fmt, ...)
{
	const char *log_prefix = "stalld: ";
	char message[1024];
	char *log;
	int kmesg_fd;
	va_list ap;

	log = message + strlen(log_prefix);

	sprintf(message, log_prefix, strlen(log_prefix));

	va_start(ap, fmt);
	vsprintf(log, fmt, ap);
	va_end(ap);

	/*
	 * print the entire message (including PREFIX).
	 */
	if (config_verbose)
		fprintf(stderr, "%s", message);

	/*
	 * print the entire message (including PREFIX).
	 */
	if (config_write_kmesg) {
		kmesg_fd = open("/dev/kmsg", O_WRONLY);

		/*
		 * Log iff possible.
		 */
		if (kmesg_fd) {
			if (write(kmesg_fd, message, strlen(message)) < 0)
				die ("write to klog failed");
			close(kmesg_fd);
		}
	}

	/*
	 * print the log (syslog adds PREFIX).
	 */
	if (config_log_syslog)
		syslog(LOG_INFO, "%s", log);

}

void write_pidfile(void)
{
	FILE *f = fopen(pidfile, "w");
	if (f != NULL) {
		fprintf(f, "%d", getpid());
		fclose(f);
	}
	else
		die("unable to open pidfile %s: %s\n", pidfile, strerror(errno));
}



/*
 * Based on:
 * https://github.com/pasce/daemon-skeleton-linux-c
 */
void deamonize(void)
{
	pid_t pid;

	/*
	 * Fork off the parent process.
	 */
	pid = fork();

	/*
	 * An error occurred.
	 */
	if (pid < 0)
		die("Error while forking the deamon");

	/*
	 * Success: Let the parent terminate.
	 */
	if (pid > 0)
		exit(EXIT_SUCCESS);

	/*
	 * On success: The child process becomes session leader.
	 */
	if (setsid() < 0)
		die("Error while creating the deamon (setsid)");

	/*
	 * Catch, ignore and handle signals.
	 * XXX: Implement a working signal handler.
	 */
	signal(SIGCHLD, SIG_IGN);
	signal(SIGHUP, SIG_IGN);

	/*
	 * Fork off for the second time.
	 */
	pid = fork();

	/*
	 * An error occurred.
	 */
	if (pid < 0)
		die("Error while forking the deamon (the second)");

	/*
	 * Success: Let the parent terminate.
	 */
	if (pid > 0)
		exit(EXIT_SUCCESS);

	/*
	 * Set new file permissions.
	 */
	umask(0);

	/*
	 * Change the working directory to the root directory.
	 */
	if (chdir("/"))
		die("Cannot change directory to '/'");
}

/*
 * Set HRTICK and frinds: Based on cyclicdeadline by Steven Rostedt.
 */
#define _STR(x) #x
#define STR(x) _STR(x)
#ifndef MAXPATH
#define MAXPATH 1024
#endif
static int find_mount(const char *mount, char *debugfs)
{
	char type[100];
	FILE *fp;

	if ((fp = fopen("/proc/mounts","r")) == NULL)
		return 0;

	while (fscanf(fp, "%*s %"
		      STR(MAXPATH)
		      "s %99s %*s %*d %*d\n",
		      debugfs, type) == 2) {
		if (strcmp(type, mount) == 0)
			break;
	}
	fclose(fp);

	if (strcmp(type, mount) != 0)
		return 0;
	return 1;
}

static const char *find_debugfs(void)
{
	static int debugfs_found;
	static char debugfs[MAXPATH+1];

	if (debugfs_found)
		return debugfs;

	if (!find_mount("debugfs", debugfs))
		return "";

	debugfs_found = 1;

	return debugfs;
}

static int setup_hr_tick(void)
{
	const char *debugfs = find_debugfs();
	char files[strlen(debugfs) + strlen("/sched_features") + 1];
	char buf[500];
	struct stat st;
	static int set = 0;
	char *p;
	int ret;
	int len;
	int fd;

	if (set)
		return 1;

	set = 1;

	if (strlen(debugfs) == 0)
		return 0;

	sprintf(files, "%s/sched_features", debugfs);
	ret = stat(files, &st);
	if (ret < 0)
		return 0;

	fd = open(files, O_RDWR);
	if (fd < 0) {
		log_msg("could not open %s to set HRTICK: %s", files, strerror(errno));
		return 0;
	}

	len = sizeof(buf);

	ret = read(fd, buf, len);
	if (ret < 0) {
		perror(files);
		close(fd);
		return 0;
	}
	if (ret >= len)
		ret = len - 1;
	buf[ret] = 0;

	ret = 1;

	p = strstr(buf, "HRTICK");
	if (p + 3 >= buf) {
		p -= 3;
		if (strncmp(p, "NO_HRTICK", 9) == 0) {
			log_msg("dl_runtime is shorter than 1ms, setting HRTICK\n");
			ret = write(fd, "HRTICK", 6);
			if (ret != 6)
				ret = 0;
			else
				ret = 1;
		}
	}

	close(fd);
	return ret;
}

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
		if (read < 0)
			goto out_close_fd;

		position += retval;

	} while (retval > 0 && position < size);

	close(fd);

	return position;

out_close_fd:
	close(fd);

out_error:
	return 0;
}

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

long get_long_from_str(char *start)
{
	long value;
	char *end;

	errno = 0;
	value = strtol(start, &end, 10);
	if (errno || start == end)
		die("Invalid ID '%s'", value);

	return value;
}

long get_long_after_colon(char *start)
{
	/*
	 * Find the ":"
	 */
	start = strstr(start, ":");
	if (!start)
		return -1;

	/*
	 * skip ":"
	 */
	start++;

	return get_long_from_str(start);
}

long get_variable_long_value(char *buffer, const char *variable)
{
	char *start;
	/*
	 * Line:
	 * '  .nr_running                    : 0'
	 */

	/*
	 * Find the ".nr_running"
	 */
	start = strstr(buffer, variable);
	if (!start)
		return -1;

	return get_long_after_colon(start);
}

/*
 * Example:
 * ' S           task   PID         tree-key  switches  prio     wait-time             sum-exec        sum-sleep'
 * '-----------------------------------------------------------------------------------------------------------'
 * ' I         rcu_gp     3        13.973264         2   100         0.000000         0.004469         0.000000 0 0 /
 */
int fill_waiting_task(char *buffer, struct task_info *task_info, int nr_entries)
{
	struct task_info *task;
	char *start = buffer;
	int tasks = 0;
	int comm_size;
	char *end;

	while (tasks < nr_entries) {
		task = &task_info[tasks];

		start = strstr(start, "\n R");

		if (!start)
			break;

		/*
		 * Skip '\n R'
		 */
		start = &start[3];

		/*
		 * skip the spaces.
		 */
		while(start[0] == ' ')
			start++;

		end = start;

		while(end[0] != ' ')
			end++;

		comm_size = end - start;

		if (comm_size > 15)
			die("comm_size is too large: %d\n", comm_size);

		strncpy(task->comm, start, comm_size);

		task->comm[comm_size] = 0;

		/*
		 * go to the end of the task comm
		 */
		start=end;

		task->pid = strtol(start, &end, 10);

		/*
		 * skip the pid
		 */
		start=end;

		/*
		 * skip the tree-key
		 */
		while(start[0] == ' ')
			start++;

		while(start[0] != ' ')
			start++;

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
	int cpu = cpu_info->id;
	char *cpu_buffer;

	cpu_buffer = alloc_and_fill_cpu_buffer(cpu, buffer, buffer_size);
	if (!cpu_buffer)
		return -1;

	cpu_info->nr_running = get_variable_long_value(cpu_buffer, ".nr_running");
	cpu_info->nr_rt_running = get_variable_long_value(cpu_buffer, ".rt_nr_running");

	cpu_info->starving = malloc(sizeof(struct task_info) * MAX_WAITING_PIDS);

	cpu_info->nr_waiting_tasks = fill_waiting_task(cpu_buffer, cpu_info->starving, MAX_WAITING_PIDS);

	if (old_tasks) {
		merge_taks_info(old_tasks, nr_old_tasks, cpu_info->starving, cpu_info->nr_waiting_tasks);
		free(old_tasks);
	}

	free(cpu_buffer);

	return 0;
}

int boost_starving_task(int pid)
{
	int ret;
	int flags = 0;
	struct sched_attr new_attr;
	struct sched_attr old_attr;

	memset(&new_attr, 0, sizeof(new_attr));
	new_attr.size = sizeof(new_attr);
	new_attr.sched_policy   = SCHED_DEADLINE;
	new_attr.sched_runtime  = config_dl_runtime;
	new_attr.sched_deadline = config_dl_period;
	new_attr.sched_period   = config_dl_period;

	/*
	 * Get the old prio, to be restored at the end of the
	 * boosting period.
	 */
	ret = sched_getattr(pid, &old_attr, sizeof(old_attr), flags);

	/*
	 * Boost.
	 */
	ret = sched_setattr(pid, &new_attr, flags);
	if (ret < 0) {
	    log_msg("sched_setattr failed to boost pid %d: %s\n", pid, strerror(errno));
	    return 1;
	}

	/*
	 * Wait.
	 *
	 * XXX: We might want to check if the task suspended before the
	 * end of the duration.
	 */
	sleep(config_boost_duration);

	/*
	 * Restore the old priority.
	 */
	ret = sched_setattr(pid, &old_attr, flags);

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
		die("checking a running thread!!!???");

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

void print_usage(void)
{
	int i;

	char *msg[] = {
		"stalld: starvation detection and avoidance (with bounds)",
		"  usage: stalld [-l] [-v] [-k] [-s] [-f] [-h] \\",
		"          [-c cpu-list] \\",
		"          [-p time in ns] [-r time in ns] \\",
		"          [-d time in seconds] [-t time in seconds]",
		"",
		"       logging options:",
		"          -l/--log_only: only log information (do not boost)",
		"          -v/--verbose: print info to the std output",
		"          -k/--log_kmsg: print log to the kernel buffer",
		"          -s/--log_syslog: print log to syslog",
		"          -f/--foreground: run in foreground [implict when -v]",
		"        boosting options:",
		"          -p/--boost_period: SCHED_DEADLINE period [ns] that the starving task will receive",
		"          -r/--boost_runtime: SCHED_DEADLINE runtime [ns] that the starving task will receive",
		"          -d/--boost_duration: how long [s] the starving task will run with SCHED_DEADLINE",
		"        monitoring options:",
		"          -t/--starving_threshold: how long [s] the starving task will wait before being boosted",
		"          -A/--aggressive_mode: dispatch one thread per run queue, even when there is no starving",
		"                               threads on all CPU (uses more CPU/power).",
		"	misc:",
		"          --pidfile: write daemon pid to specified file",
		"          -h/--help: print this menu",
		NULL,
	};

	for(i = 0; msg[i]; i++)
		fprintf(stderr, "%s\n", msg[i]);

}

void usage(const char *fmt, ...)
{
	va_list ap;

	print_usage();

	va_start(ap, fmt);
	vfprintf(stderr, fmt, ap);
	va_end(ap);

	fprintf(stderr, "\n");

	exit(EINVAL);
}

void parse_cpu_list(char *cpulist)
{
	const char *p;
	int end_cpu;
	int nr_cpus;
	int cpu;
	int i;

	nr_cpus = sysconf(_SC_NPROCESSORS_CONF);

	config_monitored_cpus = malloc(nr_cpus * sizeof(char));
	memset(config_monitored_cpus, 0, (nr_cpus * sizeof(char)));

	for (p = cpulist; *p; ) {
		cpu = atoi(p);
		if (cpu < 0 || (!cpu && *p != '0') || cpu > nr_cpus)
			goto err;

		while (isdigit(*p))
			p++;
		if (*p == '-') {
			p++;
			end_cpu = atoi(p);
			if (end_cpu < cpu || (!end_cpu && *p != '0'))
				goto err;
			while (isdigit(*p))
				p++;
		} else
			end_cpu = cpu;

		if (cpu == end_cpu) {
			if (config_verbose)
				printf("cpulist: adding cpu %d\n", cpu);
			config_monitored_cpus[cpu] = 1;
		} else {
			for (i = cpu; i <= end_cpu; i++) {
				if (config_verbose)
					printf("cpulist: adding cpu %d\n", i);
				config_monitored_cpus[i] = 1;
			}
		}

		if (*p == ',')
			p++;
	}

	return;
err:
	die("Error parsing the cpu list %s", cpulist);
}

int should_monitor(int cpu)
{
	if (config_monitor_all_cpus)
		return 1;
	if (config_monitored_cpus[cpu])
		return 1;

	return 0;
}

int parse_args(int argc, char **argv)
{
	int c;

	/* ensure the pidfile is an empty string */
	pidfile[0] = '\0';

	while (1) {
		static struct option long_options[] = {
			{"cpu",			required_argument, 0, 'c'},
			{"log_only",		no_argument,	   0, 'l'},
			{"verbose",		no_argument,	   0, 'v'},
			{"log_kmsg",		no_argument,	   0, 'k'},
			{"log_syslog",		no_argument,	   0, 's'},
			{"foreground",		no_argument,	   0, 'f'},
			{"aggressive_mode",	no_argument,	   0, 'A'},
			{"help",		no_argument,	   0, 'h'},
			{"boost_period",	required_argument, 0, 'p'},
			{"boost_runtime",	required_argument, 0, 'r'},
			{"boost_duration",	required_argument, 0, 'd'},
			{"starving_threshold",	required_argument, 0, 't'},
			{"pidfile",             required_argument, 0, 'P'},
			{0, 0, 0, 0}
		};

		/* getopt_long stores the option index here. */
		int option_index = 0;

		c = getopt_long(argc, argv, "lvkfAhsp:r:d:t:c:",
				 long_options, &option_index);

		/* Detect the end of the options. */
		if (c == -1)
			break;

		switch (c) {
		case 'c':
			config_monitor_all_cpus = 0;
			parse_cpu_list(optarg);
			break;
		case 'l':
			config_log_only = 1;
			break;
		case 'v':
			config_verbose = 1;
			config_foreground = 1;
			break;
		case 'k':
			config_write_kmesg = 1;
			break;
		case 's':
			config_log_syslog = 1;
			break;
		case 'f':
			config_foreground = 1;
			break;
		case 'A':
			config_aggressive = 1;
			break;
		case 'p':
			config_dl_period = get_long_from_str(optarg);
			if (config_dl_period < 200000000)
				usage("boost_period should be at least 200 ms");
			if (config_dl_period > 4000000000)
				usage("boost_period should be at most 4 s");
			break;
		case 'r':
			config_dl_runtime = get_long_from_str(optarg);
			if (config_dl_period < 200000000)
				usage("boost_period should be at least 200 ms");
			if (config_dl_period > 4000000000)
				usage("boost_period should be at most 4 seconds");
			break;
		case 'd':
			config_boost_duration = get_long_from_str(optarg);
			if (config_boost_duration < 1)
				usage("boost_duration should be at least 1 second");

			if (config_boost_duration > 60)
				usage("boost_duration should be at most 60 seconds");

			break;
		case 't':
			config_starving_threshold = get_long_from_str(optarg);
			if (config_starving_threshold < 1)
				usage("starving_threshold should be at least 1 second");

			if (config_starving_threshold > 3600)
				usage("boost_duration should be at most one hour");

			break;
		case 'h':
			print_usage();
			exit(EXIT_SUCCESS);
			break;
		case 'P':
			strncpy(pidfile, optarg, sizeof(pidfile)-1);
			break;
		case '?':
			usage("Invalid option");
			break;
		default:
			usage("Invalid option");
		}
	}

	if (config_dl_period < config_dl_runtime)
		usage("runtime is longer than the period");

	if (config_dl_period > (config_boost_duration * NS_PER_SEC))
		usage("the period is longer than the boost_duration: the boosted task might not be able to run");

	if (config_boost_duration > config_starving_threshold)
		usage("the boost duration cannot be longer than the starving threshold ");

	if (config_dl_runtime < 1000000)
		setup_hr_tick();

	return(0);
}

void *cpu_main(void *data)
{
	struct cpu_info *cpu = data;
	char buffer[BUFFER_SIZE];
	int nothing_to_do = 0;
	int retval;

	while (cpu->thread_running) {

		retval = read_sched_debug(buffer, BUFFER_SIZE);
		if(!retval)
			die("fail reading sched debug file!");

		parse_cpu_info(cpu, buffer, BUFFER_SIZE);

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

int aggressive_main(struct cpu_info *cpus, int nr_cpus)
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

	return 0;
}

int conservative_main(struct cpu_info *cpus, int nr_cpus)
{
	char buffer[BUFFER_SIZE];
	pthread_attr_t dettached;
	struct cpu_info *cpu;
	int retval;
	int i;

	pthread_attr_setdetachstate(&dettached, PTHREAD_CREATE_DETACHED);

	for (i = 0; i < nr_cpus; i++) {
		cpus[i].id = i;
		cpus[i].thread_running = 0;
	}

	while (1) {
		retval = read_sched_debug(buffer, BUFFER_SIZE);
		if(!retval)
			die("fail reading sched debug file!");

		for (i = 0; i < nr_cpus; i++) {
			if (!should_monitor(i))
				continue;

			cpu = &cpus[i];

			if (cpu->thread_running)
				continue;

			parse_cpu_info(cpu, buffer, BUFFER_SIZE);

			if (config_verbose)
				printf("\tchecking cpu %d - rt: %d - starving: %d\n", i, cpu->nr_rt_running, cpu->nr_waiting_tasks);

			if (check_might_starve_tasks(cpu)) {
				cpus[i].id = i;
				cpus[i].thread_running = 1;
				pthread_create(&cpus[i].thread, &dettached, cpu_main, &cpus[i]);
			}
		}

		sleep(MAX(config_starving_threshold/20,1));
	}
}


int main(int argc, char **argv)
{
	struct cpu_info *cpus;
	int nr_cpus;

	parse_args(argc, argv);

	nr_cpus = sysconf(_SC_NPROCESSORS_CONF);
	if (nr_cpus < 1)
		die("Can not calculate number of CPUS\n");

	cpus = malloc(sizeof(struct cpu_info) * nr_cpus);
	if (!cpus)
		die("Cannot allocate memory");

	memset(cpus, 0, sizeof(struct cpu_info) * nr_cpus);

	if (config_log_syslog)
		openlog("stalld", 0, LOG_DAEMON);

	if (!config_foreground)
		deamonize();

	if (strlen(pidfile) > 0)
		write_pidfile();

	if (config_aggressive)
		aggressive_main(cpus, nr_cpus);
	else
		conservative_main(cpus, nr_cpus);

	if (config_log_syslog)
		closelog();

	exit(0);
}
