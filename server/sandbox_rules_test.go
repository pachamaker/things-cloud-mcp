package main

// Pure rules of the sandbox e2e harness (sandbox_e2e_test.go, build tag e2e).
// They live in an untagged test file so that `go test ./...` checks them
// without network access (sandbox_rules_unit_test.go).

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	gosync "sync"
	"time"

	thingscloud "github.com/arthursoares/things-cloud-sdk"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

const (
	// sandboxMarkerArea must exist as an area title on the account before the
	// harness writes anything (docs/testing/sandbox-e2e.md).
	sandboxMarkerArea = "MCP E2E SANDBOX"
	// sandboxRunTitleRoot starts every title the harness writes; cleanup of
	// earlier runs matches on it.
	sandboxRunTitleRoot = "[e2e-"
	// smokeTestTitlePrefix is what the removed things_smoke_test wrote (KI-11);
	// its leftovers are still cleaned up.
	smokeTestTitlePrefix = "[smoke-test] "

	envSandboxUsername = "THINGS_SANDBOX_USERNAME"
	envSandboxPassword = "THINGS_SANDBOX_PASSWORD"
	envSandboxEnvFile  = "THINGS_SANDBOX_ENV_FILE"
	// envSandboxRunID pins the run ID, so a later CI step can clean up
	// exactly that run's objects.
	envSandboxRunID = "THINGS_E2E_RUN_ID"
	// envSandboxRequireRun turns every skip of a sandbox test into a failure.
	// CI sets it: a skipped run passes in GitHub and would look green without
	// touching the account.
	envSandboxRequireRun = "THINGS_E2E_REQUIRE_RUN"
)

// sandboxRunRequired reads envSandboxRequireRun. Any value other than empty
// or a false boolean counts as set, so a typo cannot hide a skipped run.
func sandboxRunRequired(getenv func(string) string) bool {
	raw := strings.TrimSpace(getenv(envSandboxRequireRun))
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	return err != nil || v
}

var errSandboxNotConfigured = errors.New("sandbox credentials not configured: set " +
	envSandboxEnvFile + " to a file outside the repository, or " +
	envSandboxUsername + " and " + envSandboxPassword)

type sandboxCredentials struct {
	Username string
	Password string
}

// parseEnvFile reads KEY=VALUE lines with optional `export `, blank lines,
// `#` comments and one pair of matching surrounding quotes. Errors name the
// line number only, never its content.
func parseEnvFile(r io.Reader) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(r)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("env file line %d: want KEY=VALUE", n)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	return values, nil
}

// loadSandboxCredentials takes each value from the environment first, then
// from the file named by THINGS_SANDBOX_ENV_FILE. It never puts a value into
// an error.
func loadSandboxCredentials(getenv func(string) string, readFile func(string) ([]byte, error)) (sandboxCredentials, error) {
	creds := sandboxCredentials{
		Username: strings.TrimSpace(getenv(envSandboxUsername)),
		Password: getenv(envSandboxPassword),
	}
	if path := strings.TrimSpace(getenv(envSandboxEnvFile)); path != "" {
		data, err := readFile(path)
		if err != nil {
			return sandboxCredentials{}, fmt.Errorf("%s: cannot read the file it names", envSandboxEnvFile)
		}
		values, err := parseEnvFile(bytes.NewReader(data))
		if err != nil {
			return sandboxCredentials{}, fmt.Errorf("%s: %w", envSandboxEnvFile, err)
		}
		if creds.Username == "" {
			creds.Username = strings.TrimSpace(values[envSandboxUsername])
		}
		if creds.Password == "" {
			creds.Password = values[envSandboxPassword]
		}
	}
	switch {
	case creds.Username == "" && creds.Password == "":
		return sandboxCredentials{}, errSandboxNotConfigured
	case creds.Username == "":
		return sandboxCredentials{}, fmt.Errorf("%s is not set", envSandboxUsername)
	case creds.Password == "":
		return sandboxCredentials{}, fmt.Errorf("%s is not set", envSandboxPassword)
	}
	return creds, nil
}

// checkSandboxAccount is the account guard: no write before the account has
// the marker area and is not the owner's THINGS_USERNAME account (AGENTS.md
// rule 1). verifiedEmail is what Things Cloud reports for the credentials.
func checkSandboxAccount(creds sandboxCredentials, ownerUsername, verifiedEmail string, areaTitles []string) error {
	if owner := normalizeEmail(ownerUsername); owner != "" {
		if normalizeEmail(creds.Username) == owner || normalizeEmail(verifiedEmail) == owner {
			return fmt.Errorf("refusing to write: the sandbox credentials belong to the THINGS_USERNAME account; the harness runs on the sandbox account only (AGENTS.md rule 1)")
		}
	}
	for _, title := range areaTitles {
		if title == sandboxMarkerArea {
			return nil
		}
	}
	return fmt.Errorf("refusing to write: the account has no area titled %q; create it in the Things app on the sandbox account (docs/testing/sandbox-e2e.md)", sandboxMarkerArea)
}

func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// newSandboxRunID names one harness run, e.g. e2e-20261011-1530-9f3a.
func newSandboxRunID(now time.Time, random io.Reader) (string, error) {
	b := make([]byte, 2)
	if _, err := io.ReadFull(random, b); err != nil {
		return "", fmt.Errorf("run id: %w", err)
	}
	return "e2e-" + now.UTC().Format("20060102-1504") + "-" + hex.EncodeToString(b), nil
}

func sandboxRunPrefix(runID string) string {
	return "[" + runID + "] "
}

var sandboxRunIDPattern = regexp.MustCompile(`^e2e-[A-Za-z0-9-]{1,40}$`)

func validateSandboxRunID(id string) error {
	if !sandboxRunIDPattern.MatchString(id) {
		return fmt.Errorf("%s=%q: want e2e- followed by up to 40 letters, digits or hyphens", envSandboxRunID, id)
	}
	return nil
}

// runOfTitle names the run a harness title belongs to ("e2e-…"), so logs can
// point at leftovers without printing titles.
func runOfTitle(title string) string {
	if !strings.HasPrefix(title, sandboxRunTitleRoot) {
		return "?"
	}
	run, _, ok := strings.Cut(title[1:], "]")
	if !ok {
		return "?"
	}
	return run
}

// Logs of the harness are public in CI (docs/testing/sandbox-e2e.md, "Logs"):
// they carry IDs, field names, statuses, dates and the harness's own strings,
// never a title or note read from the account.

func publicTask(o taskOutput) string {
	return fmt.Sprintf("{uuid:%s type:%s status:%s title:%d chars note:%d chars deadline:%q scheduled_for:%q completed_at:%q project_id:%q area_id:%q tags:%v}",
		o.UUID, o.Type, o.Status, len(o.Title), len(o.Note), o.Deadline, o.ScheduledFor, o.CompletedAt, o.ProjectID, o.AreaID, o.Tags)
}

func publicSDKTask(x *thingscloud.Task) string {
	if x == nil {
		return "<nil>"
	}
	day := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format("2006-01-02")
	}
	return fmt.Sprintf("{uuid:%s type:%d status:%d schedule:%d trash:%t title:%d chars note:%d chars scheduled:%q deadline:%q parents:%v areas:%v tags:%v}",
		x.UUID, x.Type, x.Status, x.Schedule, x.InTrash, len(x.Title), len(x.Note), day(x.ScheduledDate), day(x.DeadlineDate), x.ParentTaskIDs, x.AreaIDs, x.TagIDs)
}

func newSandboxRunIDNow() (string, error) {
	return newSandboxRunID(time.Now(), rand.Reader)
}

// historyPath matches the history key in Things Cloud URLs, which can show up
// in errors before the harness knows the key (KI-35).
var historyPath = regexp.MustCompile(`(/history/)[^/?"\s]+`)

// redactor replaces credentials and the history key (a bearer capability:
// history endpoints take no Authorization header) in text the harness prints.
type redactor struct {
	mu      gosync.Mutex
	secrets []string
}

func (r *redactor) add(secret string) {
	if strings.TrimSpace(secret) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secrets = append(r.secrets, secret, url.PathEscape(secret), url.QueryEscape(secret))
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
}

func (r *redactor) redact(s string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, "<redacted>")
	}
	return historyPath.ReplaceAllString(s, "${1}<redacted>")
}

// redactingWriter filters log output (server and SDK log through the
// standard logger) before it reaches the test output.
type redactingWriter struct {
	r *redactor
	w io.Writer
}

func (rw redactingWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(rw.w, rw.r.redact(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// ---------------------------------------------------------------------------
// Wire rules
// ---------------------------------------------------------------------------

// wireItem is one entity of a history commit, as sent or as read back.
type wireItem struct {
	UUID        string
	Action      int
	Kind        string
	Payload     map[string]json.RawMessage
	ServerIndex int
}

func parseCommitBody(body []byte) ([]wireItem, error) {
	var envelopes map[string]struct {
		T *int            `json:"t"`
		E string          `json:"e"`
		P json.RawMessage `json:"p"`
	}
	if err := json.Unmarshal(body, &envelopes); err != nil {
		return nil, fmt.Errorf("commit body is not a map of item envelopes: %w", err)
	}
	items := make([]wireItem, 0, len(envelopes))
	for id, env := range envelopes {
		if env.T == nil {
			return nil, fmt.Errorf("item %s has no action", id)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(env.P, &payload); err != nil {
			return nil, fmt.Errorf("item %s payload is not an object: %w", id, err)
		}
		items = append(items, wireItem{UUID: id, Action: *env.T, Kind: env.E, Payload: payload})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UUID < items[j].UUID })
	return items, nil
}

func wireItemFromHistory(it thingscloud.Item) (wireItem, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(it.P, &payload); err != nil {
		return wireItem{}, fmt.Errorf("item %s at index %d: payload is not an object: %w", it.UUID, it.ServerIndex, err)
	}
	return wireItem{UUID: it.UUID, Action: int(it.Action), Kind: string(it.Kind), Payload: payload, ServerIndex: it.ServerIndex}, nil
}

// isSafeGeneratedID is the KI-26 rule for IDs the server generates: a
// canonical 16-byte Base58 encoding of exactly 22 characters whose first byte
// is not zero (upstream EncodeUUID pads a zero byte with '1').
func isSafeGeneratedID(id string) bool {
	if len(id) != idLength {
		return false
	}
	u, err := thingscloud.DecodeUUID(id)
	return err == nil && u[0] != 0
}

var verifiedCreateKinds = map[string]bool{"Task6": true, "ChecklistItem3": true, "Area3": true, "Tag4": true, "Tombstone2": true}
var verifiedModifyKinds = map[string]bool{"Task6": true, "ChecklistItem3": true}

// wireViolations checks one item against the verified write set
// (docs/protocol/things-cloud.md §3–§6, AGENTS.md §2 and §7). today is the
// civil day the item was written for, as midnight UTC seconds.
func wireViolations(item wireItem, today int64) []string {
	var v []string
	switch item.Action {
	case 0:
		if !verifiedCreateKinds[item.Kind] {
			return []string{fmt.Sprintf("%s: create of kind %q is outside the verified write set (never Task7, KI-25)", item.UUID, item.Kind)}
		}
		if !isSafeGeneratedID(item.UUID) {
			v = append(v, fmt.Sprintf("%s: created ID is not a 22-character Base58 encoding of 16 bytes with a non-zero first byte (KI-26)", item.UUID))
		}
	case 1:
		if !verifiedModifyKinds[item.Kind] {
			return []string{fmt.Sprintf("%s: modify of kind %q is outside the verified write set", item.UUID, item.Kind)}
		}
		if thingscloud.ValidateUUID(item.UUID) != nil {
			v = append(v, fmt.Sprintf("%s: modified ID is not canonical Base58", item.UUID))
		}
	default:
		return []string{fmt.Sprintf("%s: action t=%d is outside the verified write set", item.UUID, item.Action)}
	}

	p := item.Payload
	// rr: null and rt: [] belong to the full create key set the official
	// client sends; anything else touches recurrence (KI-01, ADR-0004).
	if raw, ok := p["rr"]; ok && (item.Action != 0 || !isJSONNull(raw)) {
		v = append(v, fmt.Sprintf("%s: writes rr (KI-01)", item.UUID))
	}
	if raw, ok := p["rt"]; ok && (item.Action != 0 || !isEmptyJSONArrayOrNull(raw)) {
		v = append(v, fmt.Sprintf("%s: writes rt (KI-01)", item.UUID))
	}
	for _, key := range []string{"ix", "ti"} {
		if raw, ok := p[key]; ok {
			if n, isInt := jsonInt(raw); !isInt || n < 0 {
				v = append(v, fmt.Sprintf("%s: %s=%s is not a non-negative integer (KI-31)", item.UUID, key, raw))
			}
		}
	}
	if raw, ok := p["sp"]; ok && !isJSONNull(raw) {
		if f, isNum := jsonFloat(raw); !isNum || f <= 0 {
			v = append(v, fmt.Sprintf("%s: sp=%s; a stop date is an instant or null, never 0", item.UUID, raw))
		}
	}

	switch item.Kind {
	case "Task6":
		if item.Action == 0 {
			if tp, ok := jsonInt(p["tp"]); !ok || tp < 0 || tp > 2 {
				v = append(v, fmt.Sprintf("%s: tp=%s is not 0, 1 or 2", item.UUID, p["tp"]))
			}
		}
		if msg := placementViolation(p, today); msg != "" {
			v = append(v, fmt.Sprintf("%s: %s", item.UUID, msg))
		}
	case "Tag4":
		if raw, ok := p["sh"]; ok && !isJSONNull(raw) {
			if s, isStr := jsonString(raw); !isStr || s == "" {
				v = append(v, fmt.Sprintf("%s: tag shortcut sh=%s; null or a non-empty string", item.UUID, raw))
			}
		}
	case "Tombstone2":
		if id, ok := jsonString(p["dloid"]); !ok || thingscloud.ValidateUUID(id) != nil {
			v = append(v, fmt.Sprintf("%s: tombstone dloid=%s is not a canonical ID", item.UUID, p["dloid"]))
		}
	}
	return v
}

// placementViolation allows only the verified st/sr/tir combinations
// (docs/protocol/things-cloud.md §5.1, AGENTS.md §7).
func placementViolation(p map[string]json.RawMessage, today int64) string {
	stRaw, hasSt := p["st"]
	srRaw, hasSr := p["sr"]
	tirRaw, hasTir := p["tir"]
	if !hasSt {
		if hasSr || hasTir {
			return "sr/tir written without st is not a verified placement"
		}
		return ""
	}
	if !hasSr || !hasTir {
		return "st written without both sr and tir is not a verified placement"
	}
	st, ok := jsonInt(stRaw)
	if !ok {
		return fmt.Sprintf("st=%s is not an integer", stRaw)
	}
	srNull, tirNull := isJSONNull(srRaw), isJSONNull(tirRaw)
	sr, srOK := jsonInt(srRaw)
	tir, tirOK := jsonInt(tirRaw)
	switch {
	case st == 0 && srNull && tirNull: // Inbox
		return ""
	case st == 1 && srNull && tirNull: // Anytime
		return ""
	case st == 1 && srOK && tirOK && sr == today && tir == today: // Today
		return ""
	case st == 2 && srNull && tirNull: // Someday
		return ""
	case st == 2 && srOK && sr > today && tirNull: // Upcoming
		return ""
	}
	return fmt.Sprintf("placement st=%d sr=%s tir=%s (today %d) is not a verified combination; st=2 with today's date crashed Things", st, srRaw, tirRaw, today)
}

// itemCivilToday derives the civil day an item was written for from its own
// md or cd instant in the server's zone loc (ADR-0003), so checks done later or
// around midnight compare against the day the server used; fallback is the day
// for an item with neither. The result is the wire encoding, midnight UTC.
func itemCivilToday(p map[string]json.RawMessage, loc *time.Location, fallback civil.Date) int64 {
	for _, key := range []string{"md", "cd"} {
		if f, ok := jsonFloat(p[key]); ok && f > 0 {
			return civil.DateOf(time.Unix(int64(f), 0).In(loc)).Wire()
		}
	}
	return fallback.Wire()
}

// sandboxNearMidnight reports whether the clock is within margin of a midnight
// in loc, where a run could straddle two of the server's days.
func sandboxNearMidnight(c civil.Clock, loc *time.Location, margin time.Duration) bool {
	now := c.Now()
	today := civil.Today(c, loc)
	return now.Sub(today.Start(loc)) < margin || today.AddDays(1).Start(loc).Sub(now) < margin
}

func isJSONNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null"
}

func isEmptyJSONArrayOrNull(raw json.RawMessage) bool {
	if isJSONNull(raw) {
		return true
	}
	var arr []json.RawMessage
	return json.Unmarshal(raw, &arr) == nil && len(arr) == 0
}

func jsonInt(raw json.RawMessage) (int64, bool) {
	f, ok := jsonFloat(raw)
	if !ok || f != math.Trunc(f) {
		return 0, false
	}
	return int64(f), true
}

func jsonFloat(raw json.RawMessage) (float64, bool) {
	if isJSONNull(raw) {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}

func jsonString(raw json.RawMessage) (string, bool) {
	if isJSONNull(raw) {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// textKeys hold free text; differences in them are reported without values.
var textKeys = map[string]bool{"tt": true, "nt": true}

// payloadDifferences compares what was sent with what history returned.
// Numbers compare with a tolerance because instants are floats.
func payloadDifferences(sent, stored map[string]json.RawMessage) []string {
	var diffs []string
	keys := map[string]bool{}
	for k := range sent {
		keys[k] = true
	}
	for k := range stored {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		a, inSent := sent[k]
		b, inStored := stored[k]
		switch {
		case !inSent:
			diffs = append(diffs, fmt.Sprintf("%s: stored but not sent", k))
		case !inStored:
			diffs = append(diffs, fmt.Sprintf("%s: sent but not stored", k))
		default:
			var av, bv any
			if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil || !jsonValuesEqual(av, bv) {
				if textKeys[k] {
					diffs = append(diffs, fmt.Sprintf("%s: stored text differs from sent", k))
				} else {
					diffs = append(diffs, fmt.Sprintf("%s: sent %s, stored %s", k, a, b))
				}
			}
		}
	}
	return diffs
}

func jsonValuesEqual(a, b any) bool {
	switch av := a.(type) {
	case float64:
		bv, ok := b.(float64)
		return ok && math.Abs(av-bv) <= 1e-3
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, x := range av {
			y, ok := bv[k]
			if !ok || !jsonValuesEqual(x, y) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonValuesEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

// ---------------------------------------------------------------------------
// Write firewall
// ---------------------------------------------------------------------------

type firewallMode int

const (
	// firewallLocked: the account guard has not passed; nothing is written.
	firewallLocked firewallMode = iota
	// firewallReadOnly: read-only checks (seed list); nothing is written.
	firewallReadOnly
	// firewallWrites: creates with an allowed title prefix, and modifies or
	// tombstones of objects the run created (or cleanup registered).
	firewallWrites
)

// writeFirewall inspects every commit before it leaves the process and
// refuses anything outside the verified write set or outside the run's own
// objects, so a server bug cannot reach the sandbox history, let alone the
// seed items.
type writeFirewall struct {
	now func() time.Time
	loc *time.Location // the server's zone, for the day of a Today placement

	mu         gosync.Mutex
	mode       firewallMode
	prefixes   []string
	owned      map[string]string // uuid → kind
	violations []string
}

func newWriteFirewall(now func() time.Time, loc *time.Location, titlePrefixes ...string) *writeFirewall {
	return &writeFirewall{now: now, loc: loc, prefixes: titlePrefixes, owned: map[string]string{}}
}

func (f *writeFirewall) setMode(m firewallMode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = m
}

// own lets the run modify an object it did not create in this process
// (cleanup of earlier runs).
func (f *writeFirewall) own(uuid, kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owned[uuid] = kind
}

func (f *writeFirewall) owns(uuid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.owned[uuid]
	return ok
}

// committed records creates once Things Cloud accepted the commit.
func (f *writeFirewall) committed(items []wireItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range items {
		if it.Action == 0 && it.Kind != "Tombstone2" {
			f.owned[it.UUID] = it.Kind
		}
	}
}

func (f *writeFirewall) violationCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.violations)
}

func (f *writeFirewall) violationsSince(n int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n >= len(f.violations) {
		return nil
	}
	return append([]string(nil), f.violations[n:]...)
}

// refuse records a commit the firewall could not even parse.
func (f *writeFirewall) refuse(problem string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.violations = append(f.violations, problem)
	return fmt.Errorf("sandbox firewall refused the commit: %s", problem)
}

func (f *writeFirewall) check(items []wireItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var problems []string
	switch f.mode {
	case firewallLocked:
		problems = append(problems, "writes are locked until the sandbox account guard passes")
	case firewallReadOnly:
		problems = append(problems, "this harness run is read-only")
	default:
		for _, it := range items {
			problems = append(problems, wireViolations(it, itemCivilToday(it.Payload, f.loc, civil.Today(civil.ClockFunc(f.now), f.loc)))...)
			problems = append(problems, f.ownershipViolations(it)...)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	f.violations = append(f.violations, problems...)
	return fmt.Errorf("sandbox firewall refused the commit: %s", strings.Join(problems, "; "))
}

func (f *writeFirewall) ownershipViolations(it wireItem) []string {
	var v []string
	switch {
	case it.Kind == "Tombstone2":
		if target, _ := jsonString(it.Payload["dloid"]); f.owned[target] == "" {
			v = append(v, fmt.Sprintf("%s: tombstone for %s, which this run does not own", it.UUID, target))
		}
	case it.Action != 0 && f.owned[it.UUID] == "":
		v = append(v, fmt.Sprintf("%s: modify of an object this run does not own", it.UUID))
	}
	if raw, ok := it.Payload["tt"]; ok {
		title, _ := jsonString(raw)
		if !f.titleAllowed(title) {
			v = append(v, fmt.Sprintf("%s: title (%d chars) lacks the run prefix", it.UUID, len(title)))
		}
	} else if it.Action == 0 && it.Kind != "Tombstone2" {
		v = append(v, fmt.Sprintf("%s: create without a title", it.UUID))
	}
	return v
}

func (f *writeFirewall) titleAllowed(title string) bool {
	for _, p := range f.prefixes {
		if strings.HasPrefix(title, p) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// History folding (seed checks)
// ---------------------------------------------------------------------------

// foldedEntity is the latest value per key of one object across history.
type foldedEntity struct {
	UUID   string
	Kinds  map[string]bool
	Fields map[string]json.RawMessage
}

func (e *foldedEntity) title() string {
	s, _ := jsonString(e.Fields["tt"])
	return s
}

func foldHistory(items []wireItem) map[string]*foldedEntity {
	out := map[string]*foldedEntity{}
	for _, it := range items {
		if it.Kind == "Tombstone2" {
			if target, ok := jsonString(it.Payload["dloid"]); ok {
				delete(out, target)
			}
			continue
		}
		if it.Action == 2 {
			delete(out, it.UUID)
			continue
		}
		e := out[it.UUID]
		if e == nil || it.Action == 0 {
			e = &foldedEntity{UUID: it.UUID, Kinds: map[string]bool{}, Fields: map[string]json.RawMessage{}}
			out[it.UUID] = e
		}
		e.Kinds[it.Kind] = true
		for k, val := range it.Payload {
			e.Fields[k] = val
		}
	}
	return out
}
