package mqtt

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type Config struct {
	Broker   string
	Topic    string
	ClientID string

	OnMessage        func(topic string, payload []byte)
	OnConnectionUp   func()
	OnConnectionLost func(err error)

	Logger *slog.Logger
}

type Client struct {
	paho paho.Client
}

// Connect returns immediately; the connection is established (and forever
// re-established) in the background. The subscription is renewed on every
// (re)connect so a broker that lost its session state still delivers.
func Connect(cfg Config) *Client {
	// paho reports (re)connect failures only through these package-level
	// loggers, which are no-ops by default — without them a bad broker URL
	// or failing TLS handshake retries forever in silence.
	paho.ERROR = pahoLogger{cfg.Logger}
	paho.CRITICAL = pahoLogger{cfg.Logger}

	opts := paho.NewClientOptions().
		AddBroker(cfg.Broker).
		SetClientID(cfg.ClientID).
		SetCleanSession(false).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(time.Second).
		SetMaxReconnectInterval(30 * time.Second)

	opts.SetOnConnectHandler(func(c paho.Client) {
		if cfg.OnConnectionUp != nil {
			cfg.OnConnectionUp()
		}
		token := c.Subscribe(cfg.Topic, 1, func(_ paho.Client, m paho.Message) {
			cfg.OnMessage(m.Topic(), m.Payload())
		})
		go func() {
			token.Wait()
			if err := token.Error(); err != nil {
				cfg.Logger.Error("subscribe failed", "topic", cfg.Topic, "error", err)
			}
		}()
	})
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		if cfg.OnConnectionLost != nil {
			cfg.OnConnectionLost(err)
		}
	})

	c := paho.NewClient(opts)
	c.Connect()
	return &Client{paho: c}
}

func (c *Client) Disconnect() {
	c.paho.Disconnect(1000)
}

type pahoLogger struct {
	logger *slog.Logger
}

func (l pahoLogger) Println(v ...interface{}) {
	l.logger.Error(strings.TrimSuffix(fmt.Sprintln(v...), "\n"))
}

func (l pahoLogger) Printf(format string, v ...interface{}) {
	l.logger.Error(fmt.Sprintf(format, v...))
}
