package db

import "database/sql"

// CommitStatusPublication records only a SHA that this run actually pushed.
// Host and Repo contain no credentials. LastState is the last successful POST.
type CommitStatusPublication struct {
	SHA, Host, Repo, LastState string
	Validated                  bool
}

func (d *DB) PutCommitStatusPublication(runID string, p CommitStatusPublication) error {
	_, err := d.sql.Exec(`INSERT INTO commit_status_publications (run_id,sha,host,repo,last_state,validated) VALUES (?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET sha=excluded.sha,host=excluded.host,repo=excluded.repo,last_state=excluded.last_state,validated=excluded.validated`, runID, p.SHA, p.Host, p.Repo, p.LastState, p.Validated)
	return err
}

func (d *DB) GetCommitStatusPublication(runID string) (*CommitStatusPublication, error) {
	p := &CommitStatusPublication{}
	err := d.sql.QueryRow(`SELECT sha,host,repo,last_state,validated FROM commit_status_publications WHERE run_id=?`, runID).Scan(&p.SHA, &p.Host, &p.Repo, &p.LastState, &p.Validated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}
