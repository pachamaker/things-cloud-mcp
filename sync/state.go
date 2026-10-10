package sync

import (
	"database/sql"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
)

// State provides read-only access to the synced Things state
type State struct {
	syncer *Syncer
}

// State returns a read-only view of the current aggregated state
func (s *Syncer) State() *State {
	return &State{syncer: s}
}

func (st *State) executor() dbExecutor {
	return st.syncer.rawDB
}

// QueryOpts controls filtering for state queries
type QueryOpts struct {
	// IncludeCompleted, when false (the default), excludes both completed and
	// canceled tasks. Things treats canceled the same as completed for the
	// purpose of active-task views — neither has a dedicated "include
	// canceled" toggle, so this flag governs both.
	IncludeCompleted bool
	IncludeTrashed   bool
	Limit            int
	Offset           int
}

// Task retrieves a task by UUID
func (st *State) Task(uuid string) (*things.Task, error) {
	st.syncer.mu.RLock()
	defer st.syncer.mu.RUnlock()
	return st.syncer.readSyncer().getTask(uuid)
}

// Area retrieves an area by UUID
func (st *State) Area(uuid string) (*things.Area, error) {
	st.syncer.mu.RLock()
	defer st.syncer.mu.RUnlock()
	return st.syncer.readSyncer().getArea(uuid)
}

// Tag retrieves a tag by UUID
func (st *State) Tag(uuid string) (*things.Tag, error) {
	st.syncer.mu.RLock()
	defer st.syncer.mu.RUnlock()
	return st.syncer.readSyncer().getTag(uuid)
}

// AllTasks returns all tasks matching the query options
func (st *State) AllTasks(opts QueryOpts) ([]*things.Task, error) {
	query := `SELECT uuid FROM tasks WHERE type = 0 AND deleted = 0`
	args := []any{}
	if !opts.IncludeCompleted {
		query += " AND status NOT IN (2, 3)"
	}
	if !opts.IncludeTrashed {
		query += " AND in_trash = 0"
	}
	query += ` ORDER BY "index"`
	query, args = paginateQuery(query, args, opts)
	return st.queryTasks(query, args...)
}

// AllProjects returns all projects
func (st *State) AllProjects(opts QueryOpts) ([]*things.Task, error) {
	query := `SELECT uuid FROM tasks WHERE type = 1 AND deleted = 0`
	args := []any{}
	if !opts.IncludeCompleted {
		query += " AND status NOT IN (2, 3)"
	}
	if !opts.IncludeTrashed {
		query += " AND in_trash = 0"
	}
	query += ` ORDER BY "index"`
	query, args = paginateQuery(query, args, opts)
	return st.queryTasks(query, args...)
}

// AllAreas returns all areas
func (st *State) AllAreas() ([]*things.Area, error) {
	return st.AllAreasWithOpts(QueryOpts{})
}

// AllAreasWithOpts returns areas with optional pagination.
func (st *State) AllAreasWithOpts(opts QueryOpts) ([]*things.Area, error) {
	st.syncer.mu.RLock()
	defer st.syncer.mu.RUnlock()

	query := `SELECT uuid, title FROM areas WHERE deleted = 0 ORDER BY "index"`
	args := []any{}
	query, args = paginateQuery(query, args, opts)

	rows, err := st.executor().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var areas []*things.Area
	for rows.Next() {
		var a things.Area
		if err := rows.Scan(&a.UUID, &a.Title); err != nil {
			return nil, err
		}
		areas = append(areas, &a)
	}
	return areas, nil
}

// AllTags returns all tags
func (st *State) AllTags() ([]*things.Tag, error) {
	return st.AllTagsWithOpts(QueryOpts{})
}

// AllTagsWithOpts returns tags with optional pagination.
func (st *State) AllTagsWithOpts(opts QueryOpts) ([]*things.Tag, error) {
	st.syncer.mu.RLock()
	defer st.syncer.mu.RUnlock()

	query := `SELECT uuid, title, shortcut FROM tags WHERE deleted = 0 ORDER BY "index"`
	args := []any{}
	query, args = paginateQuery(query, args, opts)

	rows, err := st.executor().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []*things.Tag
	for rows.Next() {
		var t things.Tag
		if err := rows.Scan(&t.UUID, &t.Title, &t.ShortHand); err != nil {
			return nil, err
		}
		tags = append(tags, &t)
	}
	return tags, nil
}

// TasksInInbox returns tasks in the Inbox
func (st *State) TasksInInbox(opts QueryOpts) ([]*things.Task, error) {
	query := `SELECT uuid FROM tasks WHERE type = 0 AND schedule = 0 AND deleted = 0`
	args := []any{}
	if !opts.IncludeCompleted {
		query += " AND status NOT IN (2, 3)"
	}
	if !opts.IncludeTrashed {
		query += " AND in_trash = 0"
	}
	query += ` ORDER BY "index"`
	query, args = paginateQuery(query, args, opts)
	return st.queryTasks(query, args...)
}

// todayCondition selects the items Things shows in Today, following
// docs/protocol/things-cloud.md §5.1 (things.py today()):
//   - started (st=1) or deferred (st=2) with a start date on or before today:
//     overdue carry-over and scheduled items whose date has arrived but that no
//     Things client has confirmed yet;
//   - started and placed in Today via tir;
//   - no start date and a deadline on or before today, Inbox included.
//
// Known gaps (KI-02): a deadline dismissed from Today in the app (dds) still
// shows because dds is not mirrored; projects scheduled for today are not
// listed; This Evening is not flagged. "Today" is the UTC calendar day until
// KI-03 is fixed.
//
// Every branch is NULL-safe so that TasksInAnytime can negate it. Arguments:
// tomorrow, today, tomorrow, tomorrow (see todayConditionArgs).
const todayCondition = `(
	(schedule IN (1, 2) AND scheduled_date IS NOT NULL AND scheduled_date < ?)
	OR (schedule = 1 AND today_index_ref IS NOT NULL AND today_index_ref >= ? AND today_index_ref < ?)
	OR (scheduled_date IS NULL AND deadline_date IS NOT NULL AND deadline_date < ?)
)`

func todayConditionArgs(todayUnix, tomorrowUnix int64) []any {
	return []any{tomorrowUnix, todayUnix, tomorrowUnix, tomorrowUnix}
}

// TasksInToday returns to-dos in the Today view (see todayCondition).
func (st *State) TasksInToday(opts QueryOpts) ([]*things.Task, error) {
	todayUnix, tomorrowUnix := currentUTCDayBounds()
	return st.tasksInToday(opts, todayUnix, tomorrowUnix)
}

func (st *State) tasksInToday(opts QueryOpts, todayUnix, tomorrowUnix int64) ([]*things.Task, error) {
	query := `SELECT uuid FROM tasks WHERE type = 0 AND deleted = 0 AND ` + todayCondition
	args := todayConditionArgs(todayUnix, tomorrowUnix)
	if !opts.IncludeCompleted {
		query += " AND status NOT IN (2, 3)"
	}
	if !opts.IncludeTrashed {
		query += " AND in_trash = 0"
	}
	query += ` ORDER BY today_index, "index"`
	query, args = paginateQuery(query, args, opts)

	st.syncer.mu.RLock()
	defer st.syncer.mu.RUnlock()

	rows, err := st.executor().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return st.scanTaskUUIDs(rows)
}

// TasksInAnytime returns started (st=1) to-dos that are not in Today, so Today
// and Anytime partition st=1.
func (st *State) TasksInAnytime(opts QueryOpts) ([]*things.Task, error) {
	todayUnix, tomorrowUnix := currentUTCDayBounds()
	return st.tasksInAnytime(opts, todayUnix, tomorrowUnix)
}

func (st *State) tasksInAnytime(opts QueryOpts, todayUnix, tomorrowUnix int64) ([]*things.Task, error) {
	query := `SELECT uuid FROM tasks WHERE type = 0 AND schedule = 1 AND deleted = 0
		AND NOT ` + todayCondition
	args := todayConditionArgs(todayUnix, tomorrowUnix)
	if !opts.IncludeCompleted {
		query += " AND status NOT IN (2, 3)"
	}
	if !opts.IncludeTrashed {
		query += " AND in_trash = 0"
	}
	query += ` ORDER BY "index"`
	query, args = paginateQuery(query, args, opts)
	return st.queryTasks(query, args...)
}

// TasksInSomeday returns deferred (st=2) to-dos without a start date and
// without a future tir. Items pulled into Today by an overdue deadline stay
// here as well, as in Things.
func (st *State) TasksInSomeday(opts QueryOpts) ([]*things.Task, error) {
	_, tomorrowUnix := currentUTCDayBounds()
	return st.tasksInSomeday(opts, tomorrowUnix)
}

func (st *State) tasksInSomeday(opts QueryOpts, tomorrowUnix int64) ([]*things.Task, error) {
	query := `SELECT uuid FROM tasks WHERE type = 0 AND schedule = 2 AND deleted = 0
		AND scheduled_date IS NULL
		AND (today_index_ref IS NULL OR today_index_ref < ?)`
	args := []any{tomorrowUnix}
	if !opts.IncludeCompleted {
		query += " AND status NOT IN (2, 3)"
	}
	if !opts.IncludeTrashed {
		query += " AND in_trash = 0"
	}
	query += ` ORDER BY "index"`
	query, args = paginateQuery(query, args, opts)
	return st.queryTasks(query, args...)
}

// TasksInUpcoming returns deferred (st=2) to-dos whose start date is after
// today, or that have no start date and a future tir. A deferred to-do whose
// start date has arrived belongs to Today, not here.
func (st *State) TasksInUpcoming(opts QueryOpts) ([]*things.Task, error) {
	_, tomorrowUnix := currentUTCDayBounds()
	return st.tasksInUpcoming(opts, tomorrowUnix)
}

func (st *State) tasksInUpcoming(opts QueryOpts, tomorrowUnix int64) ([]*things.Task, error) {
	query := `SELECT uuid FROM tasks WHERE type = 0 AND schedule = 2 AND deleted = 0
		AND (
			(scheduled_date IS NOT NULL AND scheduled_date >= ?)
			OR (scheduled_date IS NULL AND today_index_ref IS NOT NULL AND today_index_ref >= ?)
		)`
	args := []any{tomorrowUnix, tomorrowUnix}
	if !opts.IncludeCompleted {
		query += " AND status NOT IN (2, 3)"
	}
	if !opts.IncludeTrashed {
		query += " AND in_trash = 0"
	}
	query += ` ORDER BY COALESCE(scheduled_date, today_index_ref), "index"`
	query, args = paginateQuery(query, args, opts)
	return st.queryTasks(query, args...)
}

// TasksInProject returns tasks belonging to a project
func (st *State) TasksInProject(projectUUID string, opts QueryOpts) ([]*things.Task, error) {
	query := `SELECT uuid FROM tasks WHERE type = 0 AND project_uuid = ? AND deleted = 0`
	args := []any{projectUUID}
	if !opts.IncludeCompleted {
		query += " AND status NOT IN (2, 3)"
	}
	if !opts.IncludeTrashed {
		query += " AND in_trash = 0"
	}
	query += ` ORDER BY "index"`
	query, args = paginateQuery(query, args, opts)

	st.syncer.mu.RLock()
	defer st.syncer.mu.RUnlock()

	rows, err := st.executor().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return st.scanTaskUUIDs(rows)
}

// TasksInArea returns tasks belonging to an area
func (st *State) TasksInArea(areaUUID string, opts QueryOpts) ([]*things.Task, error) {
	query := `SELECT uuid FROM tasks WHERE type = 0 AND area_uuid = ? AND deleted = 0`
	args := []any{areaUUID}
	if !opts.IncludeCompleted {
		query += " AND status NOT IN (2, 3)"
	}
	if !opts.IncludeTrashed {
		query += " AND in_trash = 0"
	}
	query += ` ORDER BY "index"`
	query, args = paginateQuery(query, args, opts)

	st.syncer.mu.RLock()
	defer st.syncer.mu.RUnlock()

	rows, err := st.executor().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return st.scanTaskUUIDs(rows)
}

// CompletedTasks returns completed tasks, ordered by completion date (most recent first)
func (st *State) CompletedTasks(limit int) ([]*things.Task, error) {
	return st.CompletedTasksInRange(limit, nil, nil)
}

// CompletedTasksInRange returns completed tasks in an optional completion-date window.
// completedAfter is inclusive, completedBefore is exclusive.
func (st *State) CompletedTasksInRange(limit int, completedAfter, completedBefore *time.Time) ([]*things.Task, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT uuid FROM tasks WHERE type = 0 AND status = 3 AND deleted = 0 AND in_trash = 0
		AND completion_date IS NOT NULL`
	args := []any{}
	if completedAfter != nil {
		query += " AND completion_date >= ?"
		args = append(args, completedAfter.Unix())
	}
	if completedBefore != nil {
		query += " AND completion_date < ?"
		args = append(args, completedBefore.Unix())
	}
	query += ` ORDER BY completion_date DESC LIMIT ?`
	args = append(args, limit)

	st.syncer.mu.RLock()
	defer st.syncer.mu.RUnlock()

	rows, err := st.executor().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return st.scanTaskUUIDs(rows)
}

// ChecklistItems returns checklist items for a task
func (st *State) ChecklistItems(taskUUID string) ([]*things.CheckListItem, error) {
	st.syncer.mu.RLock()
	defer st.syncer.mu.RUnlock()

	rows, err := st.executor().Query(`
			SELECT uuid, title, status, "index"
			FROM checklist_items
			WHERE task_uuid = ? AND deleted = 0
		ORDER BY "index"
	`, taskUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*things.CheckListItem
	for rows.Next() {
		var c things.CheckListItem
		var status int
		if err := rows.Scan(&c.UUID, &c.Title, &status, &c.Index); err != nil {
			return nil, err
		}
		c.Status = things.TaskStatus(status)
		c.TaskIDs = []string{taskUUID}
		items = append(items, &c)
	}
	return items, nil
}

// Helper methods

func currentUTCDayBounds() (int64, int64) {
	nowUTC := time.Now().UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	tomorrow := today.Add(24 * time.Hour)
	return today.Unix(), tomorrow.Unix()
}

func paginateQuery(query string, args []any, opts QueryOpts) (string, []any) {
	if opts.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.Limit)
		if opts.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, opts.Offset)
		}
		return query, args
	}
	if opts.Offset > 0 {
		query += " LIMIT -1 OFFSET ?"
		args = append(args, opts.Offset)
	}
	return query, args
}

func (st *State) queryTasks(query string, args ...any) ([]*things.Task, error) {
	st.syncer.mu.RLock()
	defer st.syncer.mu.RUnlock()

	rows, err := st.executor().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return st.scanTaskUUIDs(rows)
}

func (st *State) scanTaskUUIDs(rows *sql.Rows) ([]*things.Task, error) {
	var tasks []*things.Task
	syncer := st.syncer.readSyncer()

	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, err
		}
		task, err := syncer.getTask(uuid)
		if err != nil {
			return nil, err
		}
		if task != nil {
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}
