package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/infra/auth"
	"jungle-gaming-challeng/internal/infra/httpapi"
	"jungle-gaming-challeng/internal/infra/observability"
	"jungle-gaming-challeng/internal/infra/postgres"
	"jungle-gaming-challeng/internal/infra/sqs"
	"jungle-gaming-challeng/internal/worker"
)

const (
	startupCheckTimeout  = 15 * time.Second
	connectTimeout       = 5 * time.Second
	maxConnLifetime      = 30 * time.Minute
	maxConns             = 10
	requestTimeout       = 10 * time.Second
	maxBodyBytes         = 1 << 16
	readHeaderTimeout    = 5 * time.Second
	consumerConcurrency  = 10
	consumerWaitTime     = 20 * time.Second
	consumerHandleTime   = 15 * time.Second
	consumerRetryDelay   = 2 * time.Second
	outboxBatch          = 20
	outboxLease          = 30 * time.Second
	outboxInterval       = time.Second
	outboxCycleTimeout   = 20 * time.Second
	resolverBatch        = 20
	resolverInterval     = time.Second
	resolverCycleTimeout = 20 * time.Second
)

func Options(lookup func(string) string, logOutput io.Writer) fx.Option {
	cfg, err := LoadConfig(lookup)
	if err != nil {
		return fx.Error(err)
	}
	return fx.Options(
		fx.Supply(cfg),
		fx.StopTimeout(cfg.ShutdownTimeout),
		fx.WithLogger(func(logger *slog.Logger) fxevent.Logger { return &fxevent.SlogLogger{Logger: logger} }),
		fx.Module("observability", fx.Provide(
			func() *slog.Logger { return observability.NewLogger(logOutput, slog.LevelInfo) },
			observability.NewMetrics,
			func(metrics *observability.Metrics) app.Metrics { return metrics },
			newHealth,
		)),
		fx.Module("postgres", fx.Provide(
			newPool,
			func(pool *pgxpool.Pool, cfg Config) app.TxRunner {
				return postgres.NewTxRunner(pool, cfg.DBLockTimeout)
			},
			func(pool *pgxpool.Pool) app.WalletRepository { return postgres.NewWalletRepository(pool) },
			func(pool *pgxpool.Pool) app.TransactionRepository { return postgres.NewTransactionRepository(pool) },
			func(pool *pgxpool.Pool) app.LedgerRepository { return postgres.NewLedgerRepository(pool) },
			func(pool *pgxpool.Pool) app.OutboxStore { return postgres.NewOutboxStore(pool) },
			func(pool *pgxpool.Pool) app.InboxStore { return postgres.NewInboxStore(pool) },
		)),
		fx.Module("sqs", fx.Provide(newSQSClient, newQueues)),
		fx.Module("auth", fx.Provide(newVerifier)),
		fx.Module("app", fx.Provide(
			func() app.Clock { return systemClock{} },
			newService,
		)),
		fx.Module("workers",
			fx.Provide(newConsumer),
			fx.Invoke(runWorkers),
		),
		fx.Module("http",
			fx.Provide(newHTTPServer),
			fx.Invoke(runHTTPServer),
		),
		fx.Invoke(announceShutdown),
	)
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

func newPool(lc fx.Lifecycle, cfg Config, logger *slog.Logger) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), startupCheckTimeout)
	defer cancel()

	pool, err := postgres.NewPool(ctx, postgres.Config{
		URL:             cfg.DatabaseURL.Reveal(),
		MaxConns:        maxConns,
		ConnectTimeout:  connectTimeout,
		MaxConnLifetime: maxConnLifetime,
	})
	if err != nil {
		return nil, err
	}
	lc.Append(fx.StopHook(func() {
		pool.Close()
		logger.Info("database pool closed")
	}))
	return pool, nil
}

func newSQSClient(lc fx.Lifecycle, cfg Config, logger *slog.Logger) *awssqs.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	lc.Append(fx.StopHook(func() {
		transport.CloseIdleConnections()
		logger.Info("sqs connections closed")
	}))
	return sqs.NewClient(sqs.Config{
		Endpoint:        cfg.SQSEndpoint,
		Region:          cfg.AWSRegion,
		AccessKeyID:     cfg.AWSAccessKeyID.Reveal(),
		SecretAccessKey: cfg.AWSSecretAccessKey.Reveal(),
		HTTPClient:      &http.Client{Transport: transport},
	})
}

func newQueues(client *awssqs.Client) (sqs.Queues, error) {
	ctx, cancel := context.WithTimeout(context.Background(), startupCheckTimeout)
	defer cancel()
	return sqs.FindQueues(ctx, client)
}

func newVerifier(lc fx.Lifecycle, cfg Config) httpapi.TokenVerifier {
	lc.Append(fx.StartHook(func(ctx context.Context) error { return auth.Ping(ctx, cfg.OIDCKeysURL) }))
	return auth.NewVerifier(context.Background(), auth.Config{
		IssuerURL: cfg.OIDCIssuerURL,
		KeysURL:   cfg.OIDCKeysURL,
		Audience:  cfg.OIDCAudience,
	})
}

func newHealth(pool *pgxpool.Pool, client *awssqs.Client, queues sqs.Queues) *observability.Health {
	return observability.NewHealth(
		observability.Check{Name: "postgres", Ping: pool.Ping},
		observability.Check{Name: "sqs", Ping: func(ctx context.Context) error { return sqs.Ping(ctx, client, queues.InboundURL) }},
	)
}

type serviceParams struct {
	fx.In

	Tx           app.TxRunner
	Wallets      app.WalletRepository
	Transactions app.TransactionRepository
	Ledger       app.LedgerRepository
	Outbox       app.OutboxStore
	Clock        app.Clock
	Metrics      app.Metrics
	Logger       *slog.Logger
	Config       Config
}

func newService(p serviceParams) *app.Service {
	return &app.Service{
		Tx: p.Tx, Wallets: p.Wallets, Transactions: p.Transactions, Ledger: p.Ledger, Outbox: p.Outbox,
		Clock: p.Clock, Metrics: p.Metrics, Logger: p.Logger, ReferenceTTL: p.Config.PendingReferenceTTL,
	}
}

type consumerParams struct {
	fx.In

	Client  *awssqs.Client
	Queues  sqs.Queues
	Tx      app.TxRunner
	Inbox   app.InboxStore
	Service *app.Service
	Metrics app.Metrics
	Logger  *slog.Logger
}

func newConsumer(p consumerParams) *sqs.Consumer {
	handler := &sqs.Handler{Tx: p.Tx, Inbox: p.Inbox, Service: p.Service, Metrics: p.Metrics, Logger: p.Logger}
	return sqs.NewConsumer(p.Client, handler, p.Metrics, p.Logger, sqs.ConsumerConfig{
		QueueURL:       p.Queues.InboundURL,
		DLQURL:         p.Queues.InboundDLQURL,
		Concurrency:    consumerConcurrency,
		WaitTime:       consumerWaitTime,
		HandleTimeout:  consumerHandleTime,
		RetryBaseDelay: consumerRetryDelay,
	})
}

type workerParams struct {
	fx.In

	Consumer *sqs.Consumer
	Service  *app.Service
	Client   *awssqs.Client
	Queues   sqs.Queues
	Outbox   app.OutboxStore
	Metrics  app.Metrics
	Clock    app.Clock
	Logger   *slog.Logger
}

func runWorkers(lc fx.Lifecycle, p workerParams) {
	publisher := &worker.OutboxPublisher{
		Outbox: p.Outbox, Publisher: sqs.NewPublisher(p.Client, p.Queues.EventsURL), Metrics: p.Metrics,
		Clock: p.Clock, Logger: p.Logger, Batch: outboxBatch, Lease: outboxLease,
	}
	outbox := worker.Worker{
		Name: "outbox-publisher", Interval: outboxInterval, CycleTimeout: outboxCycleTimeout, Batch: outboxBatch,
		Cycle: publisher.PublishPending, Logger: p.Logger,
	}
	resolver := worker.Worker{
		Name: "pending-references", Interval: resolverInterval, CycleTimeout: resolverCycleTimeout, Batch: resolverBatch,
		Cycle: func(ctx context.Context) (int, error) {
			return p.Service.ResolveDuePendingReferences(ctx, resolverBatch)
		},
		Logger: p.Logger,
	}
	for _, run := range []func(context.Context){p.Consumer.Run, outbox.Run, resolver.Run} {
		runInBackground(lc, run)
	}
}

func runInBackground(lc fx.Lifecycle, run func(context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				run(ctx)
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancel()
			select {
			case <-done:
				return nil
			case <-stopCtx.Done():
				return stopCtx.Err()
			}
		},
	})
}

type httpParams struct {
	fx.In

	Service  *app.Service
	Verifier httpapi.TokenVerifier
	Health   *observability.Health
	Metrics  *observability.Metrics
	Logger   *slog.Logger
	Config   Config
}

func newHTTPServer(p httpParams) *http.Server {
	api := httpapi.New(httpapi.Dependencies{
		Service:        p.Service,
		Verifier:       p.Verifier,
		Readiness:      p.Health,
		Metrics:        p.Metrics.Handler(),
		Logger:         p.Logger,
		RequestTimeout: requestTimeout,
		MaxBodyBytes:   maxBodyBytes,
	})
	return &http.Server{Addr: p.Config.HTTPAddr, Handler: api.Handler(), ReadHeaderTimeout: readHeaderTimeout}
}

func runHTTPServer(lc fx.Lifecycle, server *http.Server, logger *slog.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			var listenConfig net.ListenConfig
			listener, err := listenConfig.Listen(ctx, "tcp", server.Addr)
			if err != nil {
				return err
			}
			go func() {
				if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.Error("http server failed", slog.String("error", err.Error()))
				}
			}()
			logger.Info("http server listening", slog.String("addr", listener.Addr().String()))
			return nil
		},
		OnStop: func(ctx context.Context) error {
			err := server.Shutdown(ctx)
			logger.Info("http server stopped")
			return err
		},
	})
}

func announceShutdown(lc fx.Lifecycle, health *observability.Health, logger *slog.Logger) {
	lc.Append(fx.StopHook(func() {
		health.BeginShutdown()
		logger.Info("shutdown started, readiness is now false")
	}))
}
