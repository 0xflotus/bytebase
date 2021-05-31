package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/bytebase/bytebase"
	"github.com/bytebase/bytebase/api"
	"go.uber.org/zap"
)

var (
	_ api.ProjectService = (*ProjectService)(nil)
)

// ProjectService represents a service for managing project.
type ProjectService struct {
	l  *zap.Logger
	db *DB
}

// NewProjectService returns a new project of ProjectService.
func NewProjectService(logger *zap.Logger, db *DB) *ProjectService {
	return &ProjectService{l: logger, db: db}
}

// CreateProject creates a new project.
func (s *ProjectService) CreateProject(ctx context.Context, create *api.ProjectCreate) (*api.Project, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, FormatError(err)
	}
	defer tx.Rollback()

	project, err := createProject(ctx, tx, create)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, FormatError(err)
	}

	return project, nil
}

// FindProjectList retrieves a list of projects based on find.
func (s *ProjectService) FindProjectList(ctx context.Context, find *api.ProjectFind) ([]*api.Project, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, FormatError(err)
	}
	defer tx.Rollback()

	list, err := findProjectList(ctx, tx, find)
	if err != nil {
		return []*api.Project{}, err
	}

	return list, nil
}

// FindProject retrieves a single project based on find.
// Returns ENOTFOUND if no matching record.
// Returns the first matching one and prints a warning if finding more than 1 matching records.
func (s *ProjectService) FindProject(ctx context.Context, find *api.ProjectFind) (*api.Project, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, FormatError(err)
	}
	defer tx.Rollback()

	list, err := findProjectList(ctx, tx, find)
	if err != nil {
		return nil, err
	} else if len(list) == 0 {
		return nil, &bytebase.Error{Code: bytebase.ENOTFOUND, Message: fmt.Sprintf("project not found: %v", find)}
	} else if len(list) > 1 {
		s.l.Warn(fmt.Sprintf("found mulitple projects: %d, expect 1", len(list)))
	}
	return list[0], nil
}

// PatchProject updates an existing project by ID.
// Returns ENOTFOUND if project does not exist.
func (s *ProjectService) PatchProject(ctx context.Context, patch *api.ProjectPatch) (*api.Project, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, FormatError(err)
	}
	defer tx.Rollback()

	project, err := patchProject(ctx, tx, patch)
	if err != nil {
		return nil, FormatError(err)
	}

	if err := tx.Commit(); err != nil {
		return nil, FormatError(err)
	}

	return project, nil
}

// createProject creates a new project.
func createProject(ctx context.Context, tx *Tx, create *api.ProjectCreate) (*api.Project, error) {
	// Insert row into database.
	row, err := tx.QueryContext(ctx, `
		INSERT INTO project (
			creator_id,
			updater_id,
			workspace_id,
			environment_id,
			name,
			key
		)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING id, row_status, creator_id, created_ts, updater_id, updated_ts, workspace_id, name, `+"`key`"+`
	`,
		create.CreatorId,
		create.CreatorId,
		create.WorkspaceId,
		create.Name,
		create.Key,
	)

	if err != nil {
		return nil, FormatError(err)
	}
	defer row.Close()

	row.Next()
	var project api.Project
	if err := row.Scan(
		&project.ID,
		&project.RowStatus,
		&project.CreatorId,
		&project.CreatedTs,
		&project.UpdaterId,
		&project.UpdatedTs,
		&project.WorkspaceId,
		&project.Name,
		&project.Key,
	); err != nil {
		return nil, FormatError(err)
	}

	return &project, nil
}

func findProjectList(ctx context.Context, tx *Tx, find *api.ProjectFind) (_ []*api.Project, err error) {
	// Build WHERE clause.
	where, args := []string{"1 = 1"}, []interface{}{}
	if v := find.ID; v != nil {
		where, args = append(where, "id = ?"), append(args, *v)
	}
	if v := find.RowStatus; v != nil {
		where, args = append(where, "row_status = ?"), append(args, *v)
	}
	if v := find.WorkspaceId; v != nil {
		where, args = append(where, "workspace_id = ?"), append(args, *v)
	}
	if v := find.PrincipalId; v != nil {
		where, args = append(where, "id IN (SELECT project_id FROM project_member WHERE principal_id = ?)"), append(args, *v)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT 
		    id,
			row_status,
		    creator_id,
		    created_ts,
		    updater_id,
		    updated_ts,
			workspace_id,
			name,
			key
		FROM project
		WHERE `+strings.Join(where, " AND "),
		args...,
	)
	if err != nil {
		return nil, FormatError(err)
	}
	defer rows.Close()

	// Iterate over result set and deserialize rows into list.
	list := make([]*api.Project, 0)
	for rows.Next() {
		var project api.Project
		if err := rows.Scan(
			&project.ID,
			&project.RowStatus,
			&project.CreatorId,
			&project.CreatedTs,
			&project.UpdaterId,
			&project.UpdatedTs,
			&project.WorkspaceId,
			&project.Name,
			&project.Key,
		); err != nil {
			return nil, FormatError(err)
		}

		list = append(list, &project)
	}
	if err := rows.Err(); err != nil {
		return nil, FormatError(err)
	}

	return list, nil
}

// patchProject updates a project by ID. Returns the new state of the project after update.
func patchProject(ctx context.Context, tx *Tx, patch *api.ProjectPatch) (*api.Project, error) {
	// Build UPDATE clause.
	set, args := []string{"updater_id = ?"}, []interface{}{patch.UpdaterId}
	if v := patch.RowStatus; v != nil {
		set, args = append(set, "row_status = ?"), append(args, api.RowStatus(*v))
	}
	if v := patch.Name; v != nil {
		set, args = append(set, "name = ?"), append(args, *v)
	}
	if v := patch.Key; v != nil {
		set, args = append(set, "`key` = ?"), append(args, *v)
	}

	args = append(args, patch.ID)

	// Execute update query with RETURNING.
	row, err := tx.QueryContext(ctx, `
		UPDATE project
		SET `+strings.Join(set, ", ")+`
		WHERE id = ?
		RETURNING id, row_status, creator_id, created_ts, updater_id, updated_ts, workspace_id, name, `+"`key`"+`
	`,
		args...,
	)
	if err != nil {
		return nil, FormatError(err)
	}
	defer row.Close()

	if row.Next() {
		var project api.Project
		if err := row.Scan(
			&project.ID,
			&project.RowStatus,
			&project.CreatorId,
			&project.CreatedTs,
			&project.UpdaterId,
			&project.UpdatedTs,
			&project.WorkspaceId,
			&project.Name,
			&project.Key,
		); err != nil {
			return nil, FormatError(err)
		}

		return &project, nil
	}

	return nil, &bytebase.Error{Code: bytebase.ENOTFOUND, Message: fmt.Sprintf("project ID not found: %d", patch.ID)}
}