package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"onsim/internal/app"
	"onsim/internal/auth"
	"onsim/internal/config"
	"onsim/internal/domain"
	"onsim/internal/filter"
	"onsim/internal/httpapi"
	"onsim/internal/media"
	"onsim/internal/modem"
	"onsim/internal/sipgateway"
	"onsim/internal/store"
	"onsim/internal/telegram"
)

func main() {
	cfg := config.Load()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	state, err := store.OpenWithKey(cfg.DatabasePath, cfg.MasterKeyPath)
	if err != nil {
		log.Error("open database", "error", err)
		os.Exit(1)
	}
	defer state.Close()
	initialDevice := state.Snapshot(false).Device
	initialDevice.ATPort, initialDevice.AudioPort = cfg.ATPort, cfg.AudioPort
	initialDevice.Mode = "starting"
	initialDevice.GatewayType = cfg.GatewayMode
	initialDevice.ATConnected = false
	initialDevice.AudioCapable = false
	initialDevice.Registered = false
	initialDevice.VoiceReady = false
	initialDevice.Signal = -1
	initialDevice.SignalDBm = -1
	initialDevice.Degraded = nil
	_, _ = state.UpdateDevice(initialDevice)
	authManager := auth.New(state.DB(), cfg.SessionTTL)
	var modemController modem.Controller
	if cfg.GatewayMode == "android" {
		if err = modem.GenerateAndroidToken(cfg.AndroidToken); err != nil {
			log.Error("prepare android gateway token", "error", err)
			os.Exit(1)
		}
		gateways := make([]modem.AndroidGateway, 0, len(cfg.AndroidGateways))
		for index, gateway := range cfg.AndroidGateways {
			subscriptionID := gateway.SubscriptionID
			if subscriptionID == "" {
				subscriptionID = "auto"
			}
			controlAddr, audioAddr := gateway.ControlAddr, gateway.AudioAddr
			if controlAddr == "" {
				controlAddr = fmt.Sprintf("127.0.0.1:%d", 47100+index*2)
			}
			if audioAddr == "" {
				audioAddr = fmt.Sprintf("127.0.0.1:%d", 47101+index*2)
			}
			gateways = append(gateways, modem.AndroidGateway{ID: gateway.ID, Config: modem.AndroidConfig{
				ADB: cfg.AndroidADB, ADBHome: cfg.AndroidADBHome, ADBServerSocket: cfg.AndroidADBServerSocket,
				Serial: gateway.Serial, SubscriptionID: subscriptionID, TokenPath: cfg.AndroidToken,
				ControlAddr: controlAddr, AudioAddr: audioAddr,
			}})
		}
		if len(gateways) == 0 {
			log.Error("no Android gateways configured")
			os.Exit(1)
		}
		modemController = modem.NewMultiAndroid(gateways, log)
	} else {
		modemController = modem.New(cfg.GatewayMode, cfg.ATPort, cfg.AudioPort, cfg.ControlPort, log)
	}
	filterEngine := filter.New(state)
	service := app.New(state, modemController, filterEngine, log)
	mediaManager := media.New(state, modemController, cfg.Recordings, log)
	service.SetCallMedia(mediaManager)
	mediaManager.SetMediaTimeoutHandler(func(callID string) {
		if _, err := service.Hangup(context.Background(), callID, "media_timeout"); err != nil {
			log.Warn("hang up call after browser media timeout", "call_id", callID, "error", err)
		}
	})
	bot := telegram.New(state, service, authManager, cfg.PublicURL, log)
	sipGateway, err := sipgateway.New(state, service, mediaManager, sipgateway.Config{
		Listen: cfg.SIPListen, Asterisk: cfg.SIPAsterisk, Target: cfg.SIPTarget, GeneratedConfig: cfg.AsteriskConfig,
	}, log)
	if err != nil {
		log.Error("initialize SIP gateway", "error", err)
		os.Exit(1)
	}
	service.AddObserver(bot)
	service.AddObserver(sipGateway)
	service.AddObserver(callCleanup{ended: func(call *domain.Call) {
		mediaManager.EndCall(call.ID)
	}})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	service.Start(ctx)
	bot.Start(ctx)
	sipGateway.Start(ctx)
	go monitorDisk(ctx, state, cfg.DataDir, log)
	api := httpapi.New(state, authManager, service, mediaManager, log, sipGateway)
	listen := cfg.Listen
	if cfg.TLSListen != "" {
		listen = cfg.TLSListen
	}
	server := &http.Server{Addr: listen, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 70 * time.Second, WriteTimeout: 0}
	go func() {
		log.Info("onSIM listening", "address", listen, "gateway_mode", cfg.GatewayMode, "tls", cfg.TLSListen != "")
		var serveErr error
		if cfg.TLSListen != "" {
			serveErr = server.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			serveErr = server.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			err := serveErr
			log.Error("http server", "error", err)
			cancel()
		}
	}()
	var redirectServer *http.Server
	if cfg.HTTPListen != "" && cfg.TLSListen != "" {
		redirectServer = startHTTPRedirect(cfg.HTTPListen, cfg.PublicURL, cfg.TLSCA, log)
	}
	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	if redirectServer != nil {
		_ = redirectServer.Shutdown(shutdownCtx)
	}
	log.Info("onSIM stopped")
}

func startHTTPRedirect(address, publicURL, caPath string, log *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/onsim-ca.crt", func(w http.ResponseWriter, r *http.Request) {
		if filepath.Clean(caPath) == "." {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="onsim-ca.crt"`)
		http.ServeFile(w, r, caPath)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		target := strings.TrimRight(publicURL, "/") + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		log.Info("onSIM HTTP redirect listening", "address", address)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http redirect server", "error", err)
		}
	}()
	return server
}

type callCleanup struct {
	ended func(*domain.Call)
}

func (callCleanup) IncomingCall(*domain.Call)   {}
func (callCleanup) IncomingSMS(*domain.Message) {}
func (c callCleanup) CallEnded(call *domain.Call) {
	if c.ended != nil {
		c.ended(call)
	}
}

func monitorDisk(ctx context.Context, state *store.State, path string, log *slog.Logger) {
	check := func() {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(path, &stat); err != nil {
			log.Warn("disk status", "error", err)
			return
		}
		total := float64(stat.Blocks)
		if total == 0 {
			return
		}
		used := total - float64(stat.Bavail)
		if err := state.UpdateDiskUsage(used / total * 100); err != nil {
			log.Warn("persist disk status", "error", err)
		}
	}
	check()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			check()
		case <-ctx.Done():
			return
		}
	}
}
