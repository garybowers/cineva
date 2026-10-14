package notify

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/store"
)

var logger = logging.For("notify")

// Store is what the service needs from the database.
type Store interface {
	EnabledNotifyDestinations(ctx context.Context) ([]store.NotifyDestination, error)
	RecordNotifyResult(ctx context.Context, id, errMsg string) error
	Library(ctx context.Context, id string) (store.Library, error)
	Setting(ctx context.Context, key, def string) (string, error)
}

// Settings keys.
const (
	SettingPublicURL  = "notify.public_url"
	SettingNtfyServer = "notify.ntfy_server"
)

// retryAfter are the waits before the second and third attempts.
var retryAfter = []time.Duration{10 * time.Second, time.Minute}

type job struct {
	dest    store.NotifyDestination
	msg     Message
	attempt int
}

// Service routes messages to destinations and sends them from a queue.
type Service struct {
	store Store
	http  *http.Client
	queue chan job
}

func New(st Store) *Service {
	return &Service{
		store: st,
		http:  &http.Client{Timeout: 15 * time.Second},
		queue: make(chan job, 256),
	}
}

// Run sends queued messages until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.queue:
			s.deliver(ctx, j)
		}
	}
}

func (s *Service) deliver(ctx context.Context, j job) {
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	err := send(sctx, s.http, j.dest.Kind, j.dest.Config, j.msg)
	cancel()
	var se sendError
	if err != nil && errors.As(err, &se) && se.retry && j.attempt < len(retryAfter) {
		wait := retryAfter[j.attempt]
		j.attempt++
		time.AfterFunc(wait, func() { s.enqueue(j) })
		return
	}
	msg := ""
	if err != nil {
		msg = err.Error()
		logger.Warn("notification not delivered", "destination", j.dest.Name, "event", j.msg.Event, "err", err)
	}
	if rerr := s.store.RecordNotifyResult(context.WithoutCancel(ctx), j.dest.ID, msg); rerr != nil {
		logger.Warn("recording a delivery failed", "err", rerr)
	}
}

// enqueue drops a message rather than block when the queue is full: a
// notification is worth less than whatever raised it.
func (s *Service) enqueue(j job) {
	select {
	case s.queue <- j:
	default:
		logger.Warn("notification queue full, message dropped", "destination", j.dest.Name, "event", j.msg.Event)
	}
}

// publicURL turns a path in Cineva into a link, or "" with no public
// address set.
func (s *Service) publicURL(ctx context.Context, path string) string {
	base, err := s.store.Setting(ctx, SettingPublicURL, "")
	if err != nil || base == "" || path == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + path
}

// Notice is an event to send: the message, a path in Cineva for its link,
// and for a library's titles the library they are in.
type Notice struct {
	Event     string
	Title     string
	Body      string
	Path      string
	High      bool
	LibraryID string
}

func (s *Service) message(ctx context.Context, n Notice) Message {
	return Message{Event: n.Event, Title: n.Title, Body: n.Body, URL: s.publicURL(ctx, n.Path), High: n.High}
}

// Household sends a household event to the household destinations that
// receive it. A notice naming a library's titles goes only to destinations
// allowed that library: those listed for it, or, with none listed, the
// libraries open to everyone.
func (s *Service) Household(n Notice) {
	ctx := context.Background()
	dests, err := s.store.EnabledNotifyDestinations(ctx)
	if err != nil {
		logger.Warn("reading destinations failed", "err", err)
		return
	}
	var open *bool
	msg := s.message(ctx, n)
	for _, d := range dests {
		if d.OwnerID != "" || !slices.Contains(d.Events, n.Event) {
			continue
		}
		if n.LibraryID != "" {
			if len(d.LibraryIDs) > 0 {
				if !slices.Contains(d.LibraryIDs, n.LibraryID) {
					continue
				}
			} else {
				if open == nil {
					lib, err := s.store.Library(ctx, n.LibraryID)
					o := err == nil && !lib.Restricted
					open = &o
				}
				if !*open {
					continue
				}
			}
		}
		s.enqueue(job{dest: d, msg: msg})
	}
}

// Personal sends a personal event to one user's destinations that receive
// it. Whether the user may see what it names is the caller's to check.
func (s *Service) Personal(userID string, n Notice) {
	ctx := context.Background()
	dests, err := s.store.EnabledNotifyDestinations(ctx)
	if err != nil {
		logger.Warn("reading destinations failed", "err", err)
		return
	}
	msg := s.message(ctx, n)
	for _, d := range dests {
		if d.OwnerID == userID && slices.Contains(d.Events, n.Event) {
			s.enqueue(job{dest: d, msg: msg})
		}
	}
}

// Test sends a test message to a destination now and says how it went.
func (s *Service) Test(ctx context.Context, d store.NotifyDestination) error {
	msg := s.message(ctx, Notice{Event: "test", Title: "Cineva test", Body: "Notifications from Cineva reach " + d.Name + ".", Path: "/"})
	err := send(ctx, s.http, d.Kind, d.Config, msg)
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	if rerr := s.store.RecordNotifyResult(context.WithoutCancel(ctx), d.ID, errMsg); rerr != nil {
		logger.Warn("recording a delivery failed", "err", rerr)
	}
	return err
}
