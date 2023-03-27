package app

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/metal-toolbox/conditionorc/internal/model"
	"github.com/spf13/viper"

	runtime "github.com/banzaicloud/logrus-runtime-formatter"
	"github.com/sirupsen/logrus"
)

// Config holds configuration data when running mctl
// App holds attributes for the mtl application
type App struct {
	// Viper loads configuration parameters.
	v *viper.Viper
	// The kind of application - server/orchestrator
	AppKind model.AppKind
	// Sync waitgroup to wait for running go routines on termination.
	SyncWG *sync.WaitGroup
	// Flasher configuration.
	Config *Configuration
	// TermCh is the channel to terminate the app based on a signal.
	TermCh chan os.Signal
	// Logger is the app logger.
	Logger *logrus.Logger
}

// New returns returns a new instance of the flasher app
func New(ctx context.Context, appKind model.AppKind, cfgFile string, loglevel model.LogLevel) (*App, error) {
	app := &App{
		v:       viper.New(),
		AppKind: appKind,
		Config: &Configuration{
			file: cfgFile,
		},
		SyncWG: &sync.WaitGroup{},
		Logger: logrus.New(),
		TermCh: make(chan os.Signal),
	}

	if err := app.LoadConfiguration(); err != nil {
		return nil, err
	}

	app.SetLogger(loglevel)

	// register for SIGINT, SIGTERM
	signal.Notify(app.TermCh, syscall.SIGINT, syscall.SIGTERM)

	return app, nil
}

func (a *App) SetLogger(level model.LogLevel) {
	runtimeFormatter := &runtime.Formatter{
		ChildFormatter: &logrus.JSONFormatter{},
		File:           true,
		Line:           true,
		BaseNameOnly:   true,
	}

	// set log level, format
	switch level {
	case model.LogLevelDebug:
		a.Logger.Level = logrus.DebugLevel

		// set runtime formatter options
		runtimeFormatter.BaseNameOnly = true
		runtimeFormatter.File = true
		runtimeFormatter.Line = true

	case model.LogLevelTrace:
		a.Logger.Level = logrus.TraceLevel

		// set runtime formatter options
		runtimeFormatter.File = true
		runtimeFormatter.Line = true
		runtimeFormatter.Package = true
	default:
		a.Logger.Level = logrus.InfoLevel
	}

	a.Logger.SetFormatter(runtimeFormatter)
}
