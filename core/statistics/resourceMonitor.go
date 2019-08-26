package statistics

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/ElrondNetwork/elrond-go/core"
	"github.com/shirou/gopsutil/process"
)

// ResourceMonitor outputs statistics about resources used by the binary
type ResourceMonitor struct {
	startTime time.Time
	file      *os.File
	mutFile   sync.RWMutex
}

// NewResourceMonitor creates a new ResourceMonitor instance
func NewResourceMonitor(file *os.File) (*ResourceMonitor, error) {
	if file == nil {
		return nil, ErrNilFileToWriteStats
	}

	return &ResourceMonitor{
		startTime: time.Now(),
		file:      file,
	}, nil
}

// GenerateStatistics creates a new statistic string
func (rm *ResourceMonitor) GenerateStatistics() string {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	fds := int32(0)
	numOpenFiles := 0
	numConns := 0
	proc, err := getCurrentProcess()
	if err == nil {
		fds, _ = proc.NumFDs()
		openFiles, err := proc.OpenFiles()
		if err == nil {
			numOpenFiles = len(openFiles)
		}
		conns, err := proc.Connections()
		if err == nil {
			numConns = len(conns)
		}
	}

	return fmt.Sprintf("timestamp: %d, uptime: %v, num go: %d, go mem: %s, sys mem: %s, "+
		"total mem: %s, num GC: %d, FDs: %d, num opened files: %d, num conns: %d\n",
		time.Now().Unix(),
		time.Duration(time.Now().UnixNano()-rm.startTime.UnixNano()).Round(time.Second),
		runtime.NumGoroutine(),
		core.ConvertBytes(memStats.Alloc),
		core.ConvertBytes(memStats.Sys),
		core.ConvertBytes(memStats.TotalAlloc),
		memStats.NumGC,
		fds,
		numOpenFiles,
		numConns,
	)
}

// SaveStatistics generates and saves statistic data on the disk
func (rm *ResourceMonitor) SaveStatistics() error {
	rm.mutFile.RLock()
	defer rm.mutFile.RUnlock()
	if rm.file == nil {
		return ErrNilFileToWriteStats
	}

	stats := rm.GenerateStatistics()
	_, err := rm.file.WriteString(stats)
	if err != nil {
		return err
	}

	err = rm.file.Sync()
	if err != nil {
		return err
	}

	return nil
}

// Close closes the file used for statistics
func (rm *ResourceMonitor) Close() error {
	rm.mutFile.Lock()
	defer rm.mutFile.Unlock()

	err := rm.file.Close()
	rm.file = nil
	return err
}

func getCurrentProcess() (*process.Process, error) {
	checkPid := os.Getpid()
	ret, err := process.NewProcess(int32(checkPid))
	if err != nil {
		return nil, err
	}

	return ret, nil
}
