package tuned

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"

	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	ign3types "github.com/coreos/ignition/v2/config/v3_2/types"
)

func ignitionFile(path string, mode int) ign3types.File {
	stalldBytes, err := ioutil.ReadFile(path)
	if err != nil {
		klog.Errorf("failed to read %s: %v", path, err)
		return ign3types.File{}
	}
	payload := fmt.Sprintf("data:application/octet-stream;base64,%s", base64.StdEncoding.EncodeToString(stalldBytes))

	return ign3types.File{
		Node:          ign3types.Node{Path: path},
		FileEmbedded1: ign3types.FileEmbedded1{Contents: ign3types.Resource{Source: &payload}, Mode: &mode}}
}

func stalldSystemdUnit(enabled bool) ign3types.Unit {
	const unit = `[Unit]
Description=Stall Monitor

[Service]
# List of cpus to monitor (default: all online)
# ex: CLIST="-c 1,2,5"
Environment=CLIST=

# Aggressive mode
# ex: AGGR=-A
Environment=AGGR=

# Period parameter for SCHED_DEADLINE in nanoseconds
# ex: BP="-p 1000000000"
Environment=BP="-p 1000000000"

# Runtime parameter for SCHED_DEADLINE in nanoseconds
# ex: BR="-r 20000"
Environment=BR="-r 10000"

# Duration parameter for SCHED_DEADLINE in seconds
# ex: BD="-d 3"
Environment=BD="-d 3"

# Starving Threshold in seconds
# this value the time the thread must be kept ready but not
# actually run to decide that the thread is starving
# ex: THRESH="-t 60"
# The default kernel RCU timeout value is 60s.  All available cores are preempted via a NMI when an RCU thread is starving that long.
# This means stalld must react faster.
Environment=THRESH="-t 20"

# Logging options
#
# Set logging to be some combination of:
#     --log_only
#     --log_kmsg
#     --log_syslog
#     or Nothing (default)
# ex: LOGONLY=--log_only
Environment=LOGGING="--log_syslog --log_kmsg"

# Run in the foreground
# ex: FG=--foreground
# note: when using this should change the service Type to be simple
Environment=FG=--foreground

# Write a pidfile
# ex: PF=--pidfile /run/stalld.pid
Environment=PF="--pidfile /run/stalld.pid"

ExecStartPre=/usr/local/bin/throttlectl.sh off
ExecStart=/usr/bin/chrt -f 10 /usr/local/bin/stalld --systemd $CLIST $AGGR $BP $BR $BD $THRESH $LOGGING $FG $PF
ExecStopPost=/usr/local/bin/throttlectl.sh on
Restart=always
User=root
`

	return ign3types.Unit{
		Contents: pointer.StringPtr(unit),
		Enabled:  pointer.BoolPtr(enabled),
		Name:     "stalld.service"}
}

func ProvideIgnitionFiles(stalld *bool) []ign3types.File {
	files := []ign3types.File{}

	if stalld != nil {
		files = append(files, ignitionFile("/usr/local/bin/stalld", 0755))
		files = append(files, ignitionFile("/usr/local/bin/throttlectl.sh", 0755))
	}

	return files
}

func ProvideSystemdUnits(stalld *bool) []ign3types.Unit {
	units := []ign3types.Unit{}

	if stalld != nil {
		units = append(units, stalldSystemdUnit(*stalld))
	}

	return units
}
