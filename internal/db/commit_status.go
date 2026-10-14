package db

import (
	"context"
	"database/sql"
)

// CommitStatusPublication binds reporting to a confirmed publication by this
// run. Host and Repo contain no credentials; LastState is the last successful POST.
type CommitStatusPublication struct {
	SHA, Host, Repo, LastState string
	Validated, CIEligible      bool
}

// CommitStatusAttempt is written before a remote mutation. An attempt is not
// publication proof until its exact target ref is observed at SHA.
type CommitStatusAttempt struct {
	CommitStatusPublication
	Target, Ref string
}

func (d *DB) PutCommitStatusAttempt(runID string, p CommitStatusAttempt) error {
	_, err := d.sql.Exec(`INSERT INTO commit_status_attempts (run_id,sha,host,repo,target,ref,validated,ci_eligible) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET sha=excluded.sha,host=excluded.host,repo=excluded.repo,target=excluded.target,ref=excluded.ref,validated=excluded.validated,ci_eligible=excluded.ci_eligible`, runID, p.SHA, p.Host, p.Repo, p.Target, p.Ref, p.Validated, p.CIEligible)
	return err
}

func (d *DB) GetCommitStatusAttempt(runID string) (*CommitStatusAttempt, error) {
	p := &CommitStatusAttempt{}
	err := d.sql.QueryRow(`SELECT sha,host,repo,target,ref,validated,ci_eligible FROM commit_status_attempts WHERE run_id=?`, runID).Scan(&p.SHA, &p.Host, &p.Repo, &p.Target, &p.Ref, &p.Validated, &p.CIEligible)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (d *DB) ConfirmCommitStatusAttempt(runID string, p CommitStatusPublication) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO commit_status_publications (run_id,sha,host,repo,last_state,validated,ci_eligible) VALUES (?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET sha=excluded.sha,host=excluded.host,repo=excluded.repo,last_state=excluded.last_state,validated=excluded.validated,ci_eligible=excluded.ci_eligible`, runID, p.SHA, p.Host, p.Repo, p.LastState, p.Validated, p.CIEligible); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM commit_status_attempts WHERE run_id=?`, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) PutCommitStatusPublication(runID string, p CommitStatusPublication) error {
	_, err := d.sql.Exec(`INSERT INTO commit_status_publications (run_id,sha,host,repo,last_state,validated,ci_eligible) VALUES (?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET sha=excluded.sha,host=excluded.host,repo=excluded.repo,last_state=excluded.last_state,validated=excluded.validated,ci_eligible=excluded.ci_eligible`, runID, p.SHA, p.Host, p.Repo, p.LastState, p.Validated, p.CIEligible)
	return err
}

func (d *DB) GetCommitStatusPublication(runID string) (*CommitStatusPublication, error) {
	p := &CommitStatusPublication{}
	err := d.sql.QueryRow(`SELECT sha,host,repo,last_state,validated,ci_eligible FROM commit_status_publications WHERE run_id=?`, runID).Scan(&p.SHA, &p.Host, &p.Repo, &p.LastState, &p.Validated, &p.CIEligible)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// CommitStatusRecoveryRunIDs includes terminal runs whose durable outcome has
// not reached the forge. Selection shares the reporting deadline.
func (d *DB) CommitStatusRecoveryRunIDs(ctx context.Context) ([]string, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT r.id FROM runs r LEFT JOIN commit_status_publications p ON p.run_id=r.id LEFT JOIN commit_status_attempts a ON a.run_id=r.id WHERE r.status IN ('completed','failed','cancelled') AND (a.run_id IS NOT NULL OR (p.run_id IS NOT NULL AND p.last_state != CASE WHEN r.status='completed' AND p.validated=1 THEN 'success' ELSE 'failure' END)) ORDER BY r.created_at,r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
