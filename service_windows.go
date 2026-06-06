//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"
	"wesync/internal/stmanager"
)

const serviceName = "WeSync"

func runAsService(run func()) {
	if logFile, err := openServiceLog(); err == nil {
		log.SetOutput(logFile)
		log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
		defer logFile.Close()
	}

	if err := svc.Run(serviceName, &weSyncSvc{run: run}); err != nil {
		log.Fatalf("service: %v", err)
	}
}

// openServiceLog creates/appends to data/wesync-service.log next to the exe.
func openServiceLog() (*os.File, error) {
	dir, err := stmanager.ExeDir()
	if err != nil {
		return nil, err
	}
	dataDir := filepath.Join(dir, "data")
	os.MkdirAll(dataDir, 0755) //nolint:errcheck
	path := filepath.Join(dataDir, "wesync-service.log")
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
}

type weSyncSvc struct {
	run func()
}

func (s *weSyncSvc) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				errCh <- fmt.Errorf("panic: %v", r)
			}
		}()
		s.run()
		errCh <- nil
	}()

	status <- svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown,
	}

	for {
		select {
		case err := <-errCh:
			if err != nil {
				log.Printf("service run() exited with error: %v", err)
			}
			status <- svc.Status{State: svc.StopPending}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				return false, 0
			}
		}
	}
}

func isWindowsService() bool {
	ok, _ := svc.IsWindowsService()
	return ok
}
