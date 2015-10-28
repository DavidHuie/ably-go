package ablytest

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/ably/ably-go/ably"
)

type Key struct {
	ID            string `json:"id,omitempty"`
	ScopeID       string `json:"scopeId,omitempty"`
	Status        int    `json:"status,omitempty"`
	Type          int    `json:"type,omitempty"`
	Value         string `json:"value,omitempty"`
	Created       int    `json:"created,omitempty"`
	Modified      int    `json:"modified,omitempty"`
	RawCapability string `json:"capability,omitempty"`
	Expires       int    `json:"expired,omitempty"`
	Privileged    bool   `json:"privileged,omitempty"`
}

func (k *Key) Capability() ably.Capability {
	c, _ := ably.ParseCapability(k.RawCapability)
	return c
}

type Namespace struct {
	ID        string `json:"id"`
	Created   int    `json:"created,omitempty"`
	Modified  int    `json:"modified,omitempty"`
	Persisted bool   `json:"persisted,omitempty"`
}

type Presence struct {
	ClientID string `json:"clientId"`
	Data     string `json:"data"`
	Encoding string `json:"encoding,omitempty"`
}

type Channel struct {
	Name     string     `json:"name"`
	Presence []Presence `json:"presence,omitempty"`
}

type Connection struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type Config struct {
	ID          string       `json:"id,omitempty"`
	AppID       string       `json:"appId,omitempty"`
	AccountID   string       `json:"accountId,omitempty"`
	Status      int          `json:"status,omitempty"`
	Created     int          `json:"created,omitempty"`
	Modified    int          `json:"modified,omitempty"`
	TLSOnly     bool         `json:"tlsOnly,omitempty"`
	Labels      string       `json:"labels,omitempty"`
	Keys        []Key        `json:"keys"`
	Namespaces  []Namespace  `json:"namespaces"`
	Channels    []Channel    `json:"channels"`
	Connections []Connection `json:"connections,omitempty"`
}

func DefaultConfig() *Config {
	return &Config{
		Keys: []Key{
			{},
		},
		Namespaces: []Namespace{
			{ID: "persisted", Persisted: true},
		},
		Channels: []Channel{
			{
				Name: "persisted:presence_fixtures",
				Presence: []Presence{
					{ClientID: "client_bool", Data: "true"},
					{ClientID: "client_int", Data: "true"},
					{ClientID: "client_string", Data: "true"},
					{ClientID: "client_json", Data: `{"test": "This is a JSONObject clientData payload"}`},
				},
			},
		},
	}
}

type TransportHijacker interface {
	Hijack(http.RoundTripper) http.RoundTripper
}

type Sandbox struct {
	Config      *Config
	Environment string

	client *http.Client
}

func NewRealtimeClient(opts *ably.ClientOptions) (*Sandbox, *ably.RealtimeClient) {
	app := MustSandbox(nil)
	client, err := ably.NewRealtimeClient(app.Options(opts))
	if err != nil {
		panic(nonil(err, app.Close()))
	}
	return app, client
}

func NewRestClient(opts *ably.ClientOptions) (*Sandbox, *ably.RestClient) {
	app := MustSandbox(nil)
	client, err := ably.NewRestClient(app.Options(opts))
	if err != nil {
		panic(nonil(err, app.Close()))
	}
	return app, client
}

func MustSandbox(config *Config) *Sandbox {
	app, err := NewSandbox(nil)
	if err != nil {
		panic(err)
	}
	return app
}

func NewSandbox(config *Config) (*Sandbox, error) {
	app := &Sandbox{
		Config:      config,
		Environment: nonempty(os.Getenv("ABLY_ENV"), "sandbox"),
		client:      NewHTTPClient(),
	}
	if app.Config == nil {
		app.Config = DefaultConfig()
	}
	p, err := json.Marshal(app.Config)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", app.URL("apps"), bytes.NewReader(p))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := app.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode > 299 {
		return nil, errors.New(http.StatusText(resp.StatusCode))
	}
	if err := json.NewDecoder(resp.Body).Decode(app.Config); err != nil {
		return nil, err
	}
	return app, nil
}

func (app *Sandbox) Close() error {
	req, err := http.NewRequest("DELETE", app.URL("apps", app.Config.AppID), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(app.KeyParts())
	resp, err := app.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode > 299 {
		return errors.New(http.StatusText(resp.StatusCode))
	}
	return nil
}

func (app *Sandbox) NewRealtimeClient(opts ...*ably.ClientOptions) *ably.RealtimeClient {
	client, err := ably.NewRealtimeClient(app.Options(opts...))
	if err != nil {
		panic("ably.NewRealtimeClient failed: " + err.Error())
	}
	return client
}

func (app *Sandbox) KeyParts() (name, secret string) {
	return app.Config.AppID + "." + app.Config.Keys[0].ID, app.Config.Keys[0].Value
}

func (app *Sandbox) Key() string {
	name, secret := app.KeyParts()
	return name + ":" + secret
}

func (app *Sandbox) Options(opts ...*ably.ClientOptions) *ably.ClientOptions {
	appOpts := &ably.ClientOptions{
		Environment: app.Environment,
		Protocol:    os.Getenv("ABLY_PROTOCOL"),
		HTTPClient:  NewHTTPClient(),
		AuthOptions: ably.AuthOptions{
			Key: app.Key(),
		},
	}
	opt := MergeOptions(append([]*ably.ClientOptions{{}}, opts...)...)
	// If opts want to record round trips inject the recording transport
	// via TransportHijacker interface.
	if appOpts.HTTPClient != nil && opt.HTTPClient != nil {
		if hijacked, ok := opt.HTTPClient.Transport.(TransportHijacker); ok {
			appOpts.HTTPClient.Transport = hijacked.Hijack(appOpts.HTTPClient.Transport)
			opt.HTTPClient = nil
		}
	}
	appOpts = MergeOptions(appOpts, opt)
	return appOpts
}

func (app *Sandbox) URL(paths ...string) string {
	return "https://" + app.Environment + "-rest.ably.io/" + path.Join(paths...)
}

func nonil(err ...error) error {
	for _, err := range err {
		if err != nil {
			return err
		}
	}
	return nil
}

func nonempty(s ...string) string {
	for _, s := range s {
		if s != "" {
			return s
		}
	}
	return ""
}

func NewHTTPClient() *http.Client {
	const timeout = 10 * time.Second
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			Dial: (&net.Dialer{
				Timeout:   timeout,
				KeepAlive: timeout,
			}).Dial,
			TLSHandshakeTimeout: timeout,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: os.Getenv("HTTP_PROXY") != "",
			},
		},
	}
}
