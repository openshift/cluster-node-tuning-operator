#!/usr/bin/bash

# This script is called to either turn off or turn on RT throttling
# The 'off' argument causes the current values of the throttling
# parameters from /proc/sys/kernel/sched_rt_{period,runtime}_us to
# be saved and then rt throttling to be disabled.
# The 'on' argument causes the previously saved values to be restored
# or if those are not found the defaults are re-applied.

path=/proc/sys/kernel
cmd=$1
savefile=/tmp/rtthrottle
defperiod=1000000
defruntime=950000
case ${cmd} in
    # turn off RT throttling and save previous values
    off)
	runtime=$(cat $path/sched_rt_runtime_us)
	period=$(cat $path/sched_rt_period_us)
	echo "period=$period" > $savefile
	echo "runtime=$runtime" >> $savefile
	# don't do anything if it's already disabled
	if [[ "${runtime}" != "-1" ]]; then
	    echo -1 > $path/sched_rt_runtime_us
	fi
	# verify that we really turned it off
	if [[ "$(cat $path/sched_rt_runtime_us)" != "-1" ]]; then
	    logger -t stalld "failed to turn off RT throttling"
	    exit -1
	fi
	logger -t stalld "Disabled RT throttling"
	;;

    # turn on RT throttling and restore previous values
    on)
	if [[ -f $savefile ]]; then
	    period=$(awk -F= '/^period/ {print $2}' $savefile)
	    runtime=$(awk -F= '/^runtime/ {print $2}' $savefile)
	    rm -f $savefile
	else
	    period=$defperiod
	    runtime=$defruntime
	fi
	echo $period > $path/sched_rt_period_us
	echo $runtime > $path/sched_rt_runtime_us
	logger -t stalld "Restored RT throttling"
    ;;
    show)
	echo "Period:  $(cat $path/sched_rt_period_us)"
	echo "Runtime: $(cat $path/sched_rt_runtime_us)"
	;;
    *)
	echo "usage: $0 on|off"
	exit 0
	;;
esac
