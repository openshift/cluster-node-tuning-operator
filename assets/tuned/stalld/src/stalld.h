/*
 * Data structures, constants and function prototypes
 * used by stalld
 * SPDX-License-Identifier: GPL-2.0
 *
 * Copyright (C) 2020 Red Hat Inc, Daniel Bristot de Oliveira <bristot@redhat.com>
 *
 */
#ifndef __STALLD_H__
#define __STALLD_H__

#define BUFFER_PAGES		10
#define MAX_WAITING_PIDS	30

/*
 *workqueue worker names are now more verbose and needs
 * to be taken into consideration.
 * Reference - https://lkml.org/lkml/2018/5/17/16
 * This change is also taken into consideration by
 * procps-ng
 * Commit - 2cfdbbe897f0d4e41460c7c2b92acfc5804652c8
 */
#define COMM_SIZE		63

/* macros related to the denylisting feature */
#define SWAPPER 0
#define IGNORE_THREADS 1
#define IGNORE_PROCESSES 2
#define TGID_FIELD 6
#define REGEXEC_NO_NMATCH 0
#define REGEXEC_NO_MATCHPTR NULL
#define REGEXEC_NO_FLAGS 0
#define TMP_BUFFER_SIZE 100

/*
 * this macro defines the size of a character array
 * to save strings of the form "/proc/pid/comm" or
 * "/proc/pid/status". PIDs can be configured up to
 * (2^22) on 64 bit systems which maps to 7 digits.
 * So 30 characters worth of storage should
 * be enough
 */
#define PROC_PID_FILE_PATH_LEN 30

/* Daemon umask value */
#define DAEMON_UMASK  0x133  /* 0644 */

/* informnation about running tasks on a cpu */
struct task_info {
       int pid;
       int tgid;
       int prio;
       int ctxsw;
       time_t since;
       char comm[COMM_SIZE+1];
};

/* information about cpus */
struct cpu_info {
       int id;
       int nr_running;
       int nr_rt_running;
       int ctxsw;
       int nr_waiting_tasks;
       int thread_running;
       long idle_time;
       struct task_info *starving;
       pthread_t thread;
       char *buffer;
       size_t buffer_size;
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

static inline int sched_setattr(pid_t pid, const struct sched_attr *attr,
                 unsigned int flags) {
       return syscall(__NR_sched_setattr, pid, attr, flags);
}

static inline int sched_getattr(pid_t pid, struct sched_attr *attr,
                 unsigned int size, unsigned int flags)
{
       return syscall (__NR_sched_getattr, pid , attr, size, flags);
}

#define NS_PER_SEC 1000000000
static inline void normalize_timespec(struct timespec *ts)
{
        while (ts->tv_nsec >= NS_PER_SEC) {
                ts->tv_nsec -= NS_PER_SEC;
                ts->tv_sec++;
        }
}

/*
 * forward function definitions
 */

void __die(const char *fmt, ...);
void __warn(const char *fmt, ...);
void __info(const char *fmt, ...);

#define die(fmt, ...)	__die("%s: " fmt, __func__, ##__VA_ARGS__)
#define warn(fmt, ...)	__warn("%s: " fmt, __func__, ##__VA_ARGS__)
#define info(fmt, ...)	__info("%s: " fmt, __func__, ##__VA_ARGS__)

void log_msg(const char *fmt, ...);

long get_long_from_str(char *start);
long get_long_after_colon(char *start);
long get_variable_long_value(char *buffer, const char *variable);

int setup_signal_handling(void);
void deamonize(void);
int setup_hr_tick(void);
int should_monitor(int cpu);
void usage(const char *fmt, ...);
void write_pidfile(void);
int parse_args(int argc, char **argv);
int rt_throttling_is_off(void);
int turn_off_rt_throttling(void);
void cleanup_regex();
void find_sched_debug_path(void);

/*
 * shared variables
 */
extern int running;
extern const char *version;
extern int config_verbose;
extern int config_write_kmesg;
extern int config_log_syslog;
extern int config_log_only;
extern int config_foreground;
extern int config_ignore;
extern unsigned long config_dl_period;
extern unsigned long config_dl_runtime;
extern unsigned long config_fifo_priority;
extern unsigned long config_force_fifo;
extern long config_starving_threshold;
extern long config_boost_duration;
extern long config_aggressive;
extern int config_monitor_all_cpus;
extern char *config_monitored_cpus;
extern int config_systemd;
extern long config_granularity;
extern int config_idle_detection;
extern int config_single_threaded;
extern int config_adaptive_multi_threaded;
extern char pidfile[];
extern unsigned int nr_thread_ignore;
extern unsigned int nr_process_ignore;
extern regex_t *compiled_regex_thread;
extern regex_t *compiled_regex_process;
extern char *config_sched_debug_path;

#endif /* __STALLD_H__ */
