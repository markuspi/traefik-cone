package traefik_cone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/traefik/genconf/dynamic"
)

// Config the plugin configuration.
type Config struct {
	// Expiration is the duration after which an allowlist IP expires.
	Expiration string `json:"expiration,omitempty"`
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		Expiration: "24h",
	}
}

// Provider a simple provider plugin.
type Provider struct {
	cancel     func()
	server     *http.Server
	serverAddr string
	// Map from ip to expiration time. If time is nil, the entry never expires.
	allowList     map[string]*time.Time
	allowListChan chan string
	expiration    time.Duration
}

// New creates a new Provider plugin.
func New(ctx context.Context, config *Config, name string) (*Provider, error) {
	expiration, err := time.ParseDuration(config.Expiration)
	if err != nil {
		return nil, err
	}

	return &Provider{
		serverAddr:    "",
		allowList:     map[string]*time.Time{"127.0.0.1": nil},
		allowListChan: make(chan string, 10),
		expiration:    expiration,
	}, nil
}

// Init the provider.
func (p *Provider) Init() error {
	return nil
}

// Provide creates and send dynamic configuration.
func (p *Provider) Provide(cfgChan chan<- json.Marshaler) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	// Start local HTTP server (bind to port 0 to get an OS-assigned port)
	err := p.startServer(ctx)
	if err != nil {
		return err
	}

	go func() {
		defer func() {
			if err := recover(); err != nil {
				log.Print(err)
			}
		}()

		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			configuration := p.generateConfiguration()
			cfgChan <- &dynamic.JSONPayload{Configuration: configuration}

			select {
			case ip := <-p.allowListChan:
				expiryTime := time.Now().Add(p.expiration)
				p.allowList[ip] = &expiryTime

			case <-ticker.C:
				p.purgeExpiredIps()

			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

func (p *Provider) purgeExpiredIps() {
	now := time.Now()
	for ip, t := range p.allowList {
		if t != nil && now.After(*t) {
			delete(p.allowList, ip)
		}
	}
}

func (p *Provider) startServer(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ip := r.Header.Get("X-Real-IP")
		if ip == "" {
			http.Error(w, "X-Real-IP header not found", http.StatusInternalServerError)
			return
		}

		select {
		case p.allowListChan <- ip:
			// do nothing
		case <-ctx.Done():
			// server is about to shut down -> exit immediately
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		msg := fmt.Sprintf("ip %s added to allowlist", ip)
		_, _ = w.Write([]byte(msg))
	})
	srv := &http.Server{
		Handler: mux,
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}

	// update serverAddr to the actual address (including assigned port)
	p.serverAddr = ln.Addr().String()
	p.server = srv

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server error: %v", err)
		}
	}()

	// shutdown server when context is done
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	return nil
}

func (p *Provider) getAllowListSlice() []string {
	res := make([]string, 0, len(p.allowList))
	for ip := range p.allowList {
		res = append(res, ip)
	}
	sort.Strings(res)
	return res
}

// Stop to stop the provider and the related go routines.
func (p *Provider) Stop() error {
	p.cancel()
	return nil
}

func (p *Provider) generateConfiguration() *dynamic.Configuration {
	configuration := &dynamic.Configuration{
		HTTP: &dynamic.HTTPConfiguration{
			Services:    make(map[string]*dynamic.Service),
			Middlewares: make(map[string]*dynamic.Middleware),
		},
		TCP: &dynamic.TCPConfiguration{
			Middlewares: make(map[string]*dynamic.TCPMiddleware),
		},
	}

	// Add an HTTP service that points to the local server
	url := fmt.Sprintf("http://%s", p.serverAddr)
	configuration.HTTP.Services["service"] = &dynamic.Service{
		LoadBalancer: &dynamic.ServersLoadBalancer{
			Servers: []dynamic.Server{{URL: url}},
		},
	}

	allowList := p.getAllowListSlice()

	configuration.HTTP.Middlewares["middleware"] = &dynamic.Middleware{
		IPWhiteList: &dynamic.IPWhiteList{
			SourceRange: allowList,
		},
	}

	configuration.TCP.Middlewares["middleware"] = &dynamic.TCPMiddleware{
		IPWhiteList: &dynamic.TCPIPWhiteList{
			SourceRange: allowList,
		},
	}

	return configuration
}
