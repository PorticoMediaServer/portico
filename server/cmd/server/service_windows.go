//go:build windows

package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

type windowsService struct{}

func serviceCommand() (bool, error) {
	if len(os.Args) != 2 || !strings.HasPrefix(os.Args[1], "--service") {
		return false, nil
	}
	if os.Args[1] != "--service" {
		return true, manageWindowsService(os.Args[1])
	}
	executable, err := os.Executable()
	if err != nil {
		return true, err
	}
	for key, value := range map[string]string{
		"PORTICO_DATA_DIR":   filepath.Join(env("ProgramData", `C:\ProgramData`), "Portico"),
		"PORTICO_SERVICE":    "1",
		"PORTICO_WEB_DIR":    filepath.Join(filepath.Dir(executable), "web"),
		"PORTICO_LOG_STDERR": "off",
		"PORTICO_BIND":       "[::]:32500",
	} {
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
	// SCM launches without console handles. Give the worker valid standard
	// outputs instead of asking exec to duplicate missing inherited handles.
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0600)
	if err != nil {
		return true, err
	}
	previousOut, previousErr := os.Stdout, os.Stderr
	previousLog := log.Writer()
	os.Stdout, os.Stderr = null, null
	log.SetOutput(null)
	defer func() { os.Stdout, os.Stderr = previousOut, previousErr; log.SetOutput(previousLog); null.Close() }()
	return true, svc.Run("PorticoMediaServer", windowsService{})
}

func manageWindowsService(action string) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService("PorticoMediaServer")
	created := false
	if action == "--service-install" {
		executable, exeErr := os.Executable()
		if exeErr != nil {
			return exeErr
		}
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			service, err = manager.CreateService("PorticoMediaServer", executable, mgr.Config{StartType: mgr.StartAutomatic, DisplayName: "Portico Media Server", Description: "Organizes and streams your media. Data is kept in ProgramData\\Portico.", ServiceStartName: "NT AUTHORITY\\LocalService", SidType: windows.SERVICE_SID_TYPE_UNRESTRICTED}, "--service")
			created = err == nil
		} else if err == nil {
			path, pathErr := windows.UTF16PtrFromString(windows.EscapeArg(executable) + " --service")
			if pathErr != nil {
				service.Close()
				return pathErr
			}
			// Only the executable moves. Account credentials, startup policy,
			// service SID mode and owner configuration remain with SCM.
			err = windows.ChangeServiceConfig(service.Handle, windows.SERVICE_NO_CHANGE, windows.SERVICE_NO_CHANGE, windows.SERVICE_NO_CHANGE, path, nil, nil, nil, nil, nil, nil)
		}
		if err != nil {
			if service != nil {
				service.Close()
			}
			return err
		}
		defer service.Close()
		if !created {
			return nil
		}
		if err = service.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}, {Type: mgr.ServiceRestart, Delay: 30 * time.Second}, {Type: mgr.NoAction}}, 86400); err != nil {
			return err
		}
		return service.SetRecoveryActionsOnNonCrashFailures(true)
	}
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) && (action == "--service-stop" || action == "--service-uninstall") {
		return nil
	}
	if err != nil {
		return err
	}
	defer service.Close()
	if action == "--service-start" {
		return service.Start()
	}
	if action != "--service-stop" && action != "--service-uninstall" {
		return errors.New("unknown service command")
	}
	status, err := service.Query()
	if err != nil {
		return err
	}
	var process windows.Handle
	if status.ProcessId != 0 {
		process, err = windows.OpenProcess(windows.SYNCHRONIZE, false, status.ProcessId)
		if err != nil && !errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return err
		}
		if process != 0 {
			defer windows.CloseHandle(process)
		}
	}
	if status.State != svc.Stopped {
		if _, err = service.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return err
		}
		deadline := time.Now().Add(50 * time.Second)
		for status.State != svc.Stopped {
			if time.Now().After(deadline) {
				return errors.New("Portico did not finish stopping; installation files were left in place")
			}
			time.Sleep(200 * time.Millisecond)
			status, err = service.Query()
			if err != nil {
				return err
			}
		}
	}
	// SCM can publish Stopped just before the process releases its executable.
	// Pin and join that process before an installer replaces or removes files.
	if process != 0 {
		state, err := windows.WaitForSingleObject(process, 5000)
		if err != nil {
			return err
		}
		if state != windows.WAIT_OBJECT_0 {
			return errors.New("Portico stopped but its process has not exited; installation files were left in place")
		}
	}
	if action == "--service-uninstall" {
		return service.Delete()
	}
	return nil
}

func (windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status <- svc.Status{State: svc.StartPending}
	executable, err := os.Executable()
	if err != nil {
		return true, 1
	}
	done := make(chan error, 1)
	supervise.Go("service.worker", func() { done <- supervise.Process(ctx, executable, nil) })
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				cancel()
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for checkpoint := uint32(1); ; checkpoint++ {
					status <- svc.Status{State: svc.StopPending, CheckPoint: checkpoint, WaitHint: 45000}
					select {
					case <-done:
						return false, 0
					case <-ticker.C:
					}
				}
			}
		}
	}
}
