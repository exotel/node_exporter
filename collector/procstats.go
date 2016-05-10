// Copyright 2015 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// +build !noprocstats

package collector

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

const (
	processSubsystem = "process"

// diskSectorSize uint64 = 512
)

var (
	registeredProcesses = flag.String("collector.procstats.registered-processes", "hekad",
		"Comma-separated list of processes whose statistics need to be exposed")
)

type procstatsCollector struct {
	registeredProcessesList []string
	metrics                 []prometheus.Collector
}

func init() {
	Factories["procstats"] = NewProcStatsCollector
}

// NewProcStatsCollector takes a prometheus registry and returns a new Collector exposing
// process stats based on the default process names.
func NewProcStatsCollector() (Collector, error) {
	var processLabelNames = []string{"name"}

	return &procstatsCollector{
		registeredProcessesList: strings.Split(*registeredProcesses, ","),
		metrics: []prometheus.Collector{
			prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: Namespace,
					Subsystem: processSubsystem,
					Name:      "pid",
					Help:      "The PID of the process right now",
				}, processLabelNames),
			prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: Namespace,
					Subsystem: processSubsystem,
					Name:      "mem_kilobytes",
					Help:      "The memory consumed, in bytes, by the process right now",
				}, processLabelNames),
		},
	}, nil
}

func (c *procstatsCollector) Update(ch chan<- prometheus.Metric) (err error) {

	//Iterate over all the proces names and get the PIDs from /var/run/$name.pid
	procPID := make(map[string]int, 0)
	var pid int
	var pidBytes []byte
	for _, procName := range c.registeredProcessesList {
		pidBytes, err = ioutil.ReadFile("/var/run/" + procName + ".pid")
		if err != nil {
			// log.Errorf("Unable to open the PID file for %s. Cause: %s", procName, err.Error())
			continue
		}
		pidStr := string(pidBytes[:len(pidBytes)-1])
		pid, err = strconv.Atoi(pidStr)
		if err != nil {
			log.Errorf("Failed to convert byte array to int while reading the PID for %s. Cause: %s", procName, err)
		}
		procPID[procName] = int(pid)
	}
	processStats, err := getProcessStats(procPID)
	if err != nil {
		return fmt.Errorf("couldn't get process stats: %s", err)
	}

	for procName, stats := range processStats {
		for k, value := range stats {
			if err != nil {
				return fmt.Errorf("invalid value %d in diskstats: %s", value, err)
			}
			if gauge, ok := c.metrics[k].(*prometheus.GaugeVec); ok {
				gauge.WithLabelValues(procName).Set(float64(value))
			} else {
				return fmt.Errorf("unexpected collector %d", k)
			}
		}
	}
	for _, c := range c.metrics {
		c.Collect(ch)
	}
	return err
}

func getProcessStats(procPID map[string]int) (map[string]map[int]int, error) {
	procStats := make(map[string]map[int]int, 0)
	for procName, pid := range procPID {
		pidStr := strconv.Itoa(pid)
		filename := procFilePath(pidStr) + "/status"
		var err error
		procFile, err := os.Open(filename)
		if err != nil {
			log.Errorf("Unable to open the file %s", filename)
			return procStats, err
		}
		defer procFile.Close()
		procStats[procName], err = parseProcessStats(procFile, pid)
		if err != nil {
			log.Errorf("Unable to parse the process statistics for %s", procName)
		}
	}
	return procStats, nil
}

func parseProcessStats(r io.Reader, pid int) (map[int]int, error) {
	stats := make(map[int]int, 0)
	stats[0] = pid
	var err error
	scanner := bufio.NewScanner(r)
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		//Refer: http://manpages.ubuntu.com/manpages/wily/man5/proc.5.html
		text := scanner.Text()
		procStats := strings.Split(text, ":")
		if procStats[0] == "VmRSS" {
			data := procStats[1]
			data = data[1:]
			data = strings.TrimSuffix(data, "kB")
			data = strings.TrimSpace(data)
			stats[1], err = strconv.Atoi(data)
			if err != nil {
				log.Errorf("Unable to parse the resident memory for pid: %d", pid)
				continue
			}
		}
	}
	return stats, nil
}
