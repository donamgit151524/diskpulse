package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"diskpulse/internal/app"
	"diskpulse/internal/config"
	"diskpulse/internal/notify"
	"diskpulse/internal/server"
	"diskpulse/internal/store"
	"diskpulse/internal/tray"
)

func main() {
	background := flag.Bool("background", false, "Start silently in system tray without opening browser")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	mutexHandle, alreadyRunning, err := tray.AcquireSingleInstance(`Local\DiskPulseSingleInstanceMutex`)
	if err == nil && alreadyRunning {
		// Another DiskPulse instance is already active; open its dashboard and exit.
		url := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
		_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
		return
	}
	if mutexHandle != 0 {
		defer syscall.CloseHandle(syscall.Handle(mutexHandle))
	}

	// Sync AutoStart state with actual Windows Registry entry.
	cfg.AutoStart = config.IsAutoStartEnabled()

	dbPath, err := config.DBPath()
	if err != nil {
		log.Fatalf("resolve db path: %v", err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open sqlite store: %v", err)
	}
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var tr *tray.Tray
	notifier := notify.NewDispatcher(func(title, message, severity string) {
		if tr != nil {
			tr.ShowNotification(title, message, severity)
		}
	})

	engine := app.New(cfg, st, notifier)
	srv, err := server.Start(ctx, cfg.Port, engine, st)
	if err != nil {
		log.Fatalf("start http server: %v", err)
	}
	engine.SetDashboardURL(srv.URL())

	var exiting atomic.Bool
	tr = tray.New(tray.State{
		Tooltip:   "DiskPulse — Đang khởi động...",
		Severity:  "normal",
		Paused:    cfg.Paused,
		AutoStart: cfg.AutoStart,
	}, tray.Callbacks{
		OnOpenDashboard: func() {
			engine.OpenDashboard()
		},
		OnScanNow: func() {
			engine.TriggerScan(ctx, "")
		},
		OnTogglePause: func() {
			_ = engine.TogglePause()
		},
		OnToggleAutoStart: func() {
			_ = engine.ToggleAutoStart()
		},
		OnExit: func() {
			exiting.Store(true)
			cancel()
		},
	})
	engine.AttachTray(tr)

	go engine.StartScheduler(ctx)

	// Handle Ctrl+C when launched from a terminal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		exiting.Store(true)
		cancel()
		tr.Stop()
	}()

	if !*background {
		go func() {
			time.Sleep(400 * time.Millisecond)
			if !exiting.Load() {
				engine.OpenDashboard()
			}
		}()
	}

	if err := tr.Run(); err != nil {
		log.Printf("tray loop exited: %v", err)
	}
	cancel()
}
