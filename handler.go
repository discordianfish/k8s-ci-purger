package purger

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/go-kit/kit/metrics"
	"github.com/go-kit/kit/metrics/statsd"
	"github.com/google/go-github/github"
)

type hook struct {
	*Purger
	secret []byte

	requestCounter metrics.Counter
	errorCounter   metrics.Counter
	callDuration   metrics.Histogram
}

func NewGithubHook(p *Purger, secret []byte, statsdClient *statsd.Statsd) *hook {
	return &hook{
		Purger:         p,
		secret:         secret,
		requestCounter: statsdClient.NewCounter("requests", 1.0),
		errorCounter:   statsdClient.NewCounter("errors", 1.0),
		callDuration:   statsdClient.NewTiming("duration", 1.0),
	}
}

func (h *hook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func(begin time.Time) { h.callDuration.Observe(time.Since(begin).Seconds()) }(time.Now())
	logger := log.With(logger, "client", r.RemoteAddr)
	h.requestCounter.Add(1)
	if err := h.handle(w, r); err != nil {
		h.errorCounter.Add(1)
		level.Error(logger).Log("msg", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (h *hook) handle(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not supported", http.StatusBadRequest)
		return nil
	}
	payload, err := github.ValidatePayload(r, h.secret)
	if err != nil {
		return fmt.Errorf("Couldn't read body: %s", err)
	}
	defer r.Body.Close()
	event, err := github.ParseWebHook(github.WebHookType(r), payload)
	if err != nil {
		return fmt.Errorf("Couldn't parse body: %s", err)
	}

	logger := log.With(logger, "payload", fmt.Sprintf("%v", payload))
	switch e := event.(type) {
	case *github.DeleteEvent:
		level.Debug(logger).Log("msg", "Handling DeleteEvent webhook")
		if *e.RefType != "branch" {
			level.Info(logger).Log("msg", "Ignoring delete event for refType", "refType", *e.RefType)
			http.Error(w, "Nothing to do", http.StatusOK)
			return nil
		}
		return h.Purger.Purge(*e.Repo.FullName, *e.Ref)
	default:
		http.Error(w, "Webhook not supported", http.StatusBadRequest)
		return nil
	}
}
