/*
 * stalld code to handle automatically turning off RT throttling while running
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
#include <regex.h>

#include "stalld.h"

#define RT_RUNTIME_PATH "/proc/sys/kernel/sched_rt_runtime_us"

static long rt_runtime_us = 0;

static void restore_rt_throttling(int status, void *arg)
{
	char buffer[80];
	int retval;
	int fd;

	if (rt_runtime_us != -1) {
		fd = open(RT_RUNTIME_PATH, O_WRONLY);
		if (fd < 0)
			die("failed to open %s\n", RT_RUNTIME_PATH);

		sprintf(buffer, "%ld", rt_runtime_us);

		retval = write(fd, buffer, strlen(buffer));
		if (retval < 0)
			warn("error restoring rt throttling");

		close(fd);
		log_msg("RT Throttling runtime restored to %d\n", rt_runtime_us);
	}
}

int turn_off_rt_throttling(void)
{
	char buffer[80];
	int status;
	int fd;

	/* get the current value of the throttling runtime */
	fd = open(RT_RUNTIME_PATH, O_RDWR);
	if (fd < 0)
		die("unable to open %s: %s\n", RT_RUNTIME_PATH, strerror(errno));

	status = read(fd, buffer, sizeof(buffer));
	if (status < 0)
		die("failed to read %s\n", RT_RUNTIME_PATH);

	rt_runtime_us = strtol(buffer, NULL, 10);

	if (rt_runtime_us == -1) {
		log_msg("RT throttling already disabled, doing nothing\n");
		close(fd);
		return 0;
	}

	/* turn off throttling and register an exit handler to restore it */
	status = lseek(fd, 0, SEEK_SET);
	if (status < 0)
		die("unable to seek on %s", RT_RUNTIME_PATH);
	status = write(fd, "-1", 2);
	if (status < 0)
		die("unable to write -1 to  %s", RT_RUNTIME_PATH);
	close(fd);
	on_exit(restore_rt_throttling, NULL);
	log_msg("RT Throttling disabled\n");
	return 0;
}


int rt_throttling_is_off(void)
{
	char buffer[80];
	int ret;
	int fd;

	fd = open(RT_RUNTIME_PATH, O_RDONLY);
	if (fd < 0)
		die("unable to open %s to check throttling status: %s\n",
		    RT_RUNTIME_PATH, strerror(errno));

	ret = read(fd, buffer, sizeof(buffer));
	if (ret <= 0)
		die ("unable to read %s to get runtime status: %s\n",
		     RT_RUNTIME_PATH, strerror(errno));

	close(fd);

	if (ret < sizeof(buffer))
	    buffer[ret] = '\0';

	if (buffer[ret-1] == '\n')
		buffer[ret-1] = '\0';

	if (strcmp(buffer, "-1") == 0)
		return 1;
	return 0;
}
