package worker

import (
	"context"
	"log"

	spectypes "github.com/bloxapp/ssv-spec/types"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

var (
	metricsMsgProcessing = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ssv:worker:msg:process",
		Help: "Count decided messages",
	}, []string{"prefix"})
)

func init() {
	if err := prometheus.Register(metricsMsgProcessing); err != nil {
		log.Println("could not register prometheus collector")
	}
}

// MsgHandler func that receive message.SSVMessage to handle
type MsgHandler func(msg *spectypes.SSVMessage) error

// ErrorHandler func that handles an error for a specific message
type ErrorHandler func(msg *spectypes.SSVMessage, err error) error

func defaultErrHandler(msg *spectypes.SSVMessage, err error) error {
	return err
}

// Config holds all necessary config for worker
type Config struct {
	Ctx          context.Context
	Logger       *zap.Logger
	WorkersCount int
	Buffer       int
	MetrixPrefix string
}

// Worker listen to queue and process the messages
type Worker struct {
	ctx           context.Context
	cancel        context.CancelFunc
	logger        *zap.Logger
	workersCount  int
	queue         chan *spectypes.SSVMessage
	handler       MsgHandler
	errHandler    ErrorHandler
	metricsPrefix string
}

// NewWorker return new Worker
func NewWorker(cfg *Config) *Worker {
	ctx, cancel := context.WithCancel(cfg.Ctx)
	logger := cfg.Logger.With(zap.String("who", "messageWorker"))

	w := &Worker{
		ctx:           ctx,
		cancel:        cancel,
		logger:        logger,
		workersCount:  cfg.WorkersCount,
		queue:         make(chan *spectypes.SSVMessage, cfg.Buffer),
		errHandler:    defaultErrHandler,
		metricsPrefix: cfg.MetrixPrefix,
	}

	w.init()

	return w
}

// init the worker listening process
func (w *Worker) init() {
	for i := 1; i <= w.workersCount; i++ {
		go w.startWorker(w.queue)
	}
}

// startWorker process functionality
func (w *Worker) startWorker(ch <-chan *spectypes.SSVMessage) {
	ctx, cancel := context.WithCancel(w.ctx)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			w.process(msg)
		}
	}
}

// UseHandler registers a message handler
func (w *Worker) UseHandler(handler MsgHandler) {
	w.handler = handler
}

// UseErrorHandler registers an error handler
func (w *Worker) UseErrorHandler(errHandler ErrorHandler) {
	w.errHandler = errHandler
}

// TryEnqueue tries to enqueue a job to the given job channel. Returns true if
// the operation was successful, and false if enqueuing would not have been
// possible without blocking. Job is not enqueued in the latter case.
func (w *Worker) TryEnqueue(msg *spectypes.SSVMessage) bool {
	select {
	case w.queue <- msg:
		return true
	default:
		return false
	}
}

// Close queue and worker listener
func (w *Worker) Close() {
	close(w.queue)
	w.cancel()
}

// Size returns the queue size
func (w *Worker) Size() int {
	return len(w.queue)
}

// process the msg's from queue
func (w *Worker) process(msg *spectypes.SSVMessage) {
	if w.handler == nil {
		w.logger.Warn("no handler for worker")
	}
	if err := w.handler(msg); err != nil {
		if handlerErr := w.errHandler(msg, err); handlerErr != nil {
			w.logger.Debug("failed to handle message", zap.Error(handlerErr))
			return
		}
	}
	metricsMsgProcessing.WithLabelValues(w.metricsPrefix).Inc()
}
