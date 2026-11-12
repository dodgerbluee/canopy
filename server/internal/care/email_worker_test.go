// T27 acceptance suite: quiet daily digest intents (one per owner per
// local date, refreshed in place until delivered), local-time delivery
// gates with DST normalization, SMTP delivery over an in-process fake
// server, bounded retry with the attempt log visible, at-least-once
// redelivery with a stable Message-ID after a crash between send and
// ack, and SMTP failure never blocking care. No real SMTP, no network.
package care

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care/engine"
	"canopy/server/internal/email"
)

// ---- fake SMTP ------------------------------------------------------------

type capturedMsg struct{ data string }

// fakeSMTP is a minimal in-process SMTP conversation: enough for
// net/smtp (EHLO, MAIL, RCPT, DATA, QUIT). rejectRcpt models a relay
// denial so failure paths run over a real socket, not a stub.
func fakeSMTP(t *testing.T, rejectRcpt bool) (host string, port int, got *[]capturedMsg) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	msgs := &[]capturedMsg{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSMTP(c, rejectRcpt, msgs, &mu)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	h, ps, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(ps)
	return h, p, msgs
}

func serveSMTP(c net.Conn, rejectRcpt bool, msgs *[]capturedMsg, mu *sync.Mutex) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	fmt.Fprint(w, "220 fake ESMTP\r\n")
	_ = w.Flush()
	for {
		l, err := r.ReadString('\n')
		if err != nil {
			return
		}
		up := strings.ToUpper(l)
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			fmt.Fprint(w, "250-fake\r\n250 HELP\r\n")
		case strings.HasPrefix(up, "MAIL FROM"):
			fmt.Fprint(w, "250 OK\r\n")
		case strings.HasPrefix(up, "RCPT TO"):
			if rejectRcpt {
				fmt.Fprint(w, "550 relay denied\r\n")
			} else {
				fmt.Fprint(w, "250 OK\r\n")
			}
		case strings.HasPrefix(up, "DATA"):
			fmt.Fprint(w, "354 End data with <CR><LF>.<CR><LF>\r\n")
			_ = w.Flush()
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			mu.Lock()
			*msgs = append(*msgs, capturedMsg{data: b.String()})
			mu.Unlock()
			fmt.Fprint(w, "250 OK queued\r\n")
		case strings.HasPrefix(up, "QUIT"):
			fmt.Fprint(w, "221 Bye\r\n")
			_ = w.Flush()
			return
		default:
			fmt.Fprint(w, "500 unhandled\r\n")
		}
		_ = w.Flush()
	}
}

// dailyWaterRule is due on every weekday so the digest always has a
// task on the day the suite runs (the PLAN_START anchor would otherwise
// schedule the first slot one interval out).
func dailyWaterRule(vid string) RuleInput {
	return RuleInput{
		RuleKey: "water-daily", Kind: "ACTION", ActivityKind: ptr("WATER"),
		Recurrence: RecurrenceSpecInput{WeeklyDays: &WeeklyDaysInput{Weekdays: []string{
			"MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY", "FRIDAY", "SATURDAY", "SUNDAY"}}},
		GuidanceText:     "Deep soak; let the top few inches dry between waterings.",
		SourceVersionIDs: []string{vid},
	}
}

// ---- helpers ---------------------------------------------------------------

func emailRows(t *testing.T, pool *pgxpool.Pool) []dbgen.Notification {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		"SELECT * FROM notifications WHERE channel = 'EMAIL' ORDER BY created_at, id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []dbgen.Notification
	for rows.Next() {
		var n dbgen.Notification
		if err := rows.Scan(&n.ID, &n.WorkspaceID, &n.OwnerID, &n.Channel, &n.DedupKey,
			&n.RefKind, &n.RefID, &n.Title, &n.Body, &n.DigestDate, &n.ReadAt, &n.CreatedAt,
			&n.ScheduledAt, &n.MessageID, &n.DeliveredAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func attemptRows(t *testing.T, pool *pgxpool.Pool, notif pgtype.UUID) (states []string, codes []string) {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		"SELECT state, error_code FROM delivery_attempts WHERE notification_id = $1 ORDER BY id", notif)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var ec *string
		if err := rows.Scan(&st, &ec); err != nil {
			t.Fatal(err)
		}
		states = append(states, st)
		codes = append(codes, deref(ec))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return states, codes
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---- tests -----------------------------------------------------------------

func TestDigestQuietDedupAndRefresh(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)

	// Quiet default: no due work, no digest.
	if err := s.EnsureDigests(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if n := len(emailRows(t, pool)); n != 0 {
		t.Fatalf("EMAIL intents = %d, want 0 with nothing due", n)
	}

	plant := mkPlant(t, pool, a)
	vid := publishedVersion(t, s, a, dailyWaterRule(mkSource(t, s, a, "Watering guide").Versions[0].ID))
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	if err := s.MaterializePlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.EnsureDigests(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rows := emailRows(t, pool)
	if len(rows) != 1 {
		t.Fatalf("EMAIL intents = %d, want exactly 1 digest", len(rows))
	}
	d := rows[0]
	today := engine.DateFrom(time.Now().UTC()) // scratch workspace tz is UTC
	if want := fmt.Sprintf("digest:%s:%s:EMAIL", uuidString(a.OwnerID), today.String()); d.DedupKey != want {
		t.Fatalf("dedup key = %q, want %q", d.DedupKey, want)
	}
	if !d.DigestDate.Valid || pgToEng(d.DigestDate) != today {
		t.Fatalf("digestDate = %v, want %s", d.DigestDate.Time, today)
	}
	// Gate opens at the plan notification time (schema default 09:00) in
	// the workspace timezone.
	if want := today.String() + "T09:00:00Z"; d.ScheduledAt.Time.UTC().Format(time.RFC3339) != want {
		t.Fatalf("scheduledAt = %v, want %s", d.ScheduledAt.Time, want)
	}
	if !strings.Contains(d.Body, "Watering: "+plant.Name) {
		t.Fatalf("digest body = %q, want the due task line", d.Body)
	}
	if !d.MessageID.Valid || !strings.HasSuffix(d.MessageID.String, "@canopy") {
		t.Fatalf("messageID = %v, want stable @canopy identity", d.MessageID.String)
	}

	// Duplicate composition passes never add a second digest for the day.
	if err := s.EnsureDigests(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rows2 := emailRows(t, pool)
	if len(rows2) != 1 {
		t.Fatalf("EMAIL intents = %d after re-run, want 1 (never two digests/day)", len(rows2))
	}
	if rows2[0].ID != d.ID {
		t.Fatal("digest row must refresh in place, not be replaced")
	}

	// A second plant's due task rides the SAME digest (refreshed body).
	plant2 := mkPlant(t, pool, a)
	plan2 := mkPlan(t, s, a, uuidString(plant2.ID), vid)
	if err := s.MaterializePlan(ctx, plan2.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureDigests(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rows3 := emailRows(t, pool)
	if len(rows3) != 1 {
		t.Fatalf("EMAIL intents = %d, want 1 (late task rides the same digest)", len(rows3))
	}
	if !strings.Contains(rows3[0].Body, plant.Name) || !strings.Contains(rows3[0].Body, plant2.Name) {
		t.Fatalf("refreshed body = %q, want both tasks", rows3[0].Body)
	}
}

func TestEmailDeliveryFakeSMTP(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := publishedVersion(t, s, a, dailyWaterRule(mkSource(t, s, a, "Watering guide").Versions[0].ID))
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	if err := s.MaterializePlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureDigests(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	d := emailRows(t, pool)[0]

	host, port, got := fakeSMTP(t, false)
	s.Email = &email.SMTPSender{Host: host, Port: port, From: "canopy@canopy.invalid", Timeout: 5 * time.Second}
	s.EmailTo = "owner@canopy.invalid"

	// Gate closed before the digest time: nothing goes out.
	if err := s.RunEmailDelivery(ctx, d.ScheduledAt.Time.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 0 {
		t.Fatal("delivery must wait for the local-time gate")
	}

	if err := s.RunEmailDelivery(ctx, d.ScheduledAt.Time.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("sends = %d, want 1", len(*got))
	}
	wire := (*got)[0].data
	if !strings.Contains(wire, "Message-ID: <"+d.MessageID.String+">") {
		t.Fatalf("wire missing stable Message-ID:\n%s", wire)
	}
	if !strings.Contains(wire, "Content-Type: text/plain") || strings.Contains(wire, "Content-Type: multipart") {
		t.Fatalf("digest email must be plain text, no media:\n%s", wire)
	}
	if !strings.Contains(wire, "Subject: "+d.Title) || !strings.Contains(wire, "Watering: "+plant.Name) {
		t.Fatalf("wire missing subject/body:\n%s", wire)
	}
	states, _ := attemptRows(t, pool, d.ID)
	if len(states) != 1 || states[0] != "SENT" {
		t.Fatalf("attempts = %v, want one SENT", states)
	}
	var delivered bool
	if err := pool.QueryRow(ctx,
		"SELECT delivered_at IS NOT NULL FROM notifications WHERE id = $1", d.ID).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("intent must be acked after a successful send")
	}

	// Delivered intents are never re-sent. (Gate-open time, not wall
	// clock: the suite must not depend on the hour it runs at.)
	if err := s.RunEmailDelivery(ctx, d.ScheduledAt.Time.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("sends = %d after ack, want 1 (no double send)", len(*got))
	}

	// Crash between send and ack (attempt log rolled back): the next pass
	// redelivers the SAME Message-ID — the disclosed at-least-once channel.
	if _, err := pool.Exec(ctx,
		"UPDATE notifications SET delivered_at = NULL WHERE id = $1", d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		"DELETE FROM delivery_attempts WHERE notification_id = $1", d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RunEmailDelivery(ctx, d.ScheduledAt.Time.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("sends = %d, want 2 (at-least-once after crash-before-ack)", len(*got))
	}
	if !strings.Contains((*got)[1].data, "Message-ID: <"+d.MessageID.String+">") {
		t.Fatal("redelivery must keep the stable Message-ID")
	}
}

func TestEmailFailureVisibleBoundedNeverBlocksCare(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := publishedVersion(t, s, a, dailyWaterRule(mkSource(t, s, a, "Watering guide").Versions[0].ID))
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	if err := s.MaterializePlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureDigests(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	d := emailRows(t, pool)[0]

	host, port, got := fakeSMTP(t, true) // relay denial
	s.Email = &email.SMTPSender{Host: host, Port: port, From: "canopy@canopy.invalid", Timeout: 5 * time.Second}
	s.EmailTo = "owner@canopy.invalid"

	now := d.ScheduledAt.Time.Add(time.Hour)
	for i := 0; i < 6; i++ {
		if err := s.RunEmailDelivery(ctx, now); err != nil {
			t.Fatalf("SMTP failure must not fail the pass: %v", err)
		}
	}
	states, codes := attemptRows(t, pool, d.ID)
	if len(states) != int(emailMaxAttempts) {
		t.Fatalf("attempts = %d, want the bounded cap %d", len(states), emailMaxAttempts)
	}
	for i := range states {
		if states[i] != "FAILED" || codes[i] != "rcpt" {
			t.Fatalf("attempt %d = %s/%s, want FAILED/rcpt", i, states[i], codes[i])
		}
	}
	if len(*got) != 0 {
		t.Fatal("rejected RCPT must never reach DATA")
	}
	// Care is never blocked by email failure: the agenda still reads.
	conn, probs, err := s.CareAgenda(ctx, a, nil, nil, nil, 50, nil)
	noProbs(t, probs, err)
	if len(conn.Items) == 0 {
		t.Fatal("agenda empty — care must proceed despite SMTP failure")
	}
}

func TestLocalInstantDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// Spring-forward gap: 2026-03-08 02:30 does not exist; the contract
	// moves it to the next valid instant — the transition itself.
	got := localInstant(ny, engine.Date{Year: 2026, Month: 3, Day: 8}, 2, 30)
	if want := time.Date(2026, 3, 8, 3, 0, 0, 0, ny); !got.Equal(want) {
		t.Fatalf("nonexistent 02:30 -> %v, want %v (next valid instant)", got, want)
	}
	// Fall-back fold: 2026-11-01 01:30 is ambiguous; the contract picks
	// the earlier offset (EDT).
	got = localInstant(ny, engine.Date{Year: 2026, Month: 11, Day: 1}, 1, 30)
	if want := time.Date(2026, 11, 1, 1, 30, 0, 0, time.FixedZone("EDT", -4*3600)); !got.Equal(want) {
		t.Fatalf("ambiguous 01:30 -> %v (offset %d), want EDT -4h", got, offsetSecs(got))
	}
	// Ordinary day: plain wall-clock resolution.
	got = localInstant(ny, engine.Date{Year: 2026, Month: 11, Day: 2}, 9, 0)
	if want := time.Date(2026, 11, 2, 9, 0, 0, 0, ny); !got.Equal(want) {
		t.Fatalf("09:00 -> %v, want %v", got, want)
	}
}

func offsetSecs(t time.Time) int {
	_, off := t.Zone()
	return off
}
