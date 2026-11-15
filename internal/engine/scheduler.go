package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/frheg/athena/internal/store"
)

// Notifier sends a message out of Athena (mail now, other channels later). Optional.
type Notifier interface {
	Send(ctx context.Context, owner, subject, body string) error
}

// Scheduler runs jobs on their cron schedule. A job is a prompt that runs as a turn with principal job:<name>, in a
// new conversation of its own. What the job may do on its own is set in the policy (jobs.allow); everything else is
// queued as an approval, and you are notified.
type Scheduler struct {
	E        *Engine
	Notifier Notifier
	// Reminders, when set, returns the reminders that are due (and marks them handed out). Each becomes a
	// notification for its subject, and a mail.
	Reminders func(ctx context.Context) ([]DueReminder, error)
	every     time.Duration
}

// DueReminder is a reminder to deliver now.
type DueReminder struct {
	Owner, Text string
	DueAt       time.Time
}

// NewScheduler hooks the scheduler into the engine's turn events.
func NewScheduler(e *Engine, n Notifier) *Scheduler {
	s := &Scheduler{E: e, Notifier: n, every: time.Minute}
	e.OnDone = s.turnDone
	e.OnWait = s.turnWaits
	return s
}

// Run checks the jobs at the start of every minute until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	for {
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		s.tick(ctx, next.In(s.E.Zone))
	}
}

func (s *Scheduler) tick(ctx context.Context, at time.Time) {
	s.reminders(ctx)
	jobs, err := s.E.Store.Jobs(ctx, "")
	if err != nil {
		s.E.Log.Error("scheduler: list jobs", "err", err)
		return
	}
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		c, err := ParseCron(j.Schedule)
		if err != nil || !c.Match(at) {
			continue
		}
		if j.LastRunAt != nil && j.LastRunAt.Truncate(time.Minute).Equal(at.Truncate(time.Minute)) {
			continue
		}
		if _, err := s.RunJob(ctx, j); err != nil {
			s.E.Log.Error("scheduler: run job", "job", j.Name, "err", err)
		}
	}
}

// RunJob starts a job now. It returns the new conversation's id.
func (s *Scheduler) RunJob(ctx context.Context, j store.Job) (string, error) {
	now := time.Now()
	sess, err := s.E.Store.CreateSession(ctx, NewID(), j.Owner, "job:"+j.Name, fmt.Sprintf("%s, %s", j.Name, now.In(s.E.Zone).Format("2006-01-02 15:04")))
	if err != nil {
		return "", err
	}
	_ = s.E.Store.JobRan(ctx, j.ID, now, "running")
	if _, err := s.E.Start(ctx, j.Owner, sess.ID, "job:"+j.Name, j.Prompt); err != nil {
		_ = s.E.Store.JobRan(ctx, j.ID, now, "failed: "+err.Error())
		return sess.ID, err
	}
	return sess.ID, nil
}

func (s *Scheduler) job(t store.Turn) (store.Job, bool) {
	name, ok := strings.CutPrefix(t.Principal, "job:")
	if !ok {
		return store.Job{}, false
	}
	jobs, err := s.E.Store.Jobs(context.Background(), t.Owner)
	if err != nil {
		return store.Job{}, false
	}
	for _, j := range jobs {
		if j.Name == name {
			return j, true
		}
	}
	return store.Job{}, false
}

func (s *Scheduler) turnDone(t store.Turn, content string) {
	j, ok := s.job(t)
	if !ok {
		return
	}
	ctx := context.Background()
	status, title, body := "done", j.Name, content
	if t.Status == "failed" {
		status, title, body = "failed", j.Name+" failed", t.Error
	}
	_ = s.E.Store.JobRan(ctx, j.ID, time.Now(), status)
	s.notify(ctx, t, j.Notify, title, body)
}

func (s *Scheduler) turnWaits(t store.Turn, n int) {
	j, ok := s.job(t)
	if !ok {
		return
	}
	ctx := context.Background()
	_ = s.E.Store.JobRan(ctx, j.ID, time.Now(), "waiting for approval")
	s.notify(ctx, t, j.Notify, j.Name+" waits for you", fmt.Sprintf("The job %q wants to do %d thing(s) it may not do on its own. Open Athena to approve or refuse.", j.Name, n))
}

func (s *Scheduler) notify(ctx context.Context, t store.Turn, out bool, title, body string) {
	id, err := s.E.Store.Notify(ctx, t.Owner, title, body, &t.SessionID)
	if err != nil {
		s.E.Log.Error("store notification", "err", err)
		return
	}
	s.E.Hub.Publish(OwnerStream(t.Owner), "notification", store.Notification{ID: id, At: time.Now(), Title: title, Body: body, SessionID: &t.SessionID})
	if out && s.Notifier != nil {
		if err := s.Notifier.Send(ctx, t.Owner, "Athena: "+title, body); err != nil {
			s.E.Log.Warn("notify failed", "err", err)
			s.E.audit(ctx, t.Owner, t.Principal, t.SessionID, t.ID, "notify.fail", "", map[string]string{"error": err.Error()})
		} else {
			s.E.audit(ctx, t.Owner, t.Principal, t.SessionID, t.ID, "notify.sent", "", map[string]string{"subject": title})
		}
	}
}

func (s *Scheduler) reminders(ctx context.Context) {
	if s.Reminders == nil {
		return
	}
	due, err := s.Reminders(ctx)
	if err != nil {
		s.E.Log.Warn("scheduler: reminders", "err", err)
		return
	}
	for _, r := range due {
		title := "Reminder: " + r.Text
		if rr := []rune(title); len(rr) > 80 {
			title = string(rr[:77]) + "..."
		}
		body := r.Text + "\n\n(set for " + r.DueAt.In(s.E.Zone).Format("Mon 2 Jan 15:04") + ")"
		id, err := s.E.Store.Notify(ctx, r.Owner, title, body, nil)
		if err != nil {
			s.E.Log.Error("store reminder", "err", err)
			continue
		}
		_ = s.E.Store.Audit(ctx, r.Owner, "scheduler", nil, nil, "reminder.fire", "", map[string]string{"text": r.Text})
		s.E.Hub.Publish(OwnerStream(r.Owner), "notification", store.Notification{ID: id, At: time.Now(), Title: title, Body: body})
		if s.Notifier != nil {
			if err := s.Notifier.Send(ctx, r.Owner, "Athena: "+title, body); err != nil {
				s.E.Log.Warn("reminder mail failed", "err", err)
			}
		}
	}
}
