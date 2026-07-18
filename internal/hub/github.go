package hub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/githubapp"
	repositoryscanner "github.com/barrikadelabs/barrikade-lens/internal/scanner/repository"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	delivery, event, signature := r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-GitHub-Event"), r.Header.Get("X-Hub-Signature-256")
	if delivery == "" || event == "" || signature == "" {
		writeError(w, 400, "invalid_webhook", "Required GitHub webhook headers are missing")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "invalid_webhook", "Webhook body could not be read")
		return
	}
	mac := hmac.New(sha256.New, s.config.GitHubWebhookSecret)
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		writeError(w, 401, "invalid_signature", "GitHub webhook signature is invalid")
		return
	}
	tx, err := s.config.Pool.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "database_error", "Could not process webhook")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `INSERT INTO github_webhook_deliveries(delivery_id,event_type) VALUES($1,$2) ON CONFLICT DO NOTHING`, delivery, event)
	if err != nil {
		writeError(w, 500, "database_error", "Could not record webhook")
		return
	}
	if tag.RowsAffected() == 0 {
		writeJSON(w, 200, map[string]string{"status": "duplicate"})
		return
	}
	switch event {
	case "installation":
		var payload struct {
			Action       string `json:"action"`
			Installation struct {
				ID      int64 `json:"id"`
				Account struct {
					Login string `json:"login"`
				} `json:"account"`
			} `json:"installation"`
			Repositories []struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repositories"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.Installation.ID == 0 {
			writeError(w, 400, "invalid_webhook", "Installation payload is malformed")
			return
		}
		if payload.Action == "deleted" {
			err = removeInstallationSources(r.Context(), tx, payload.Installation.ID)
			if err == nil {
				_, err = tx.Exec(r.Context(), `DELETE FROM github_installations WHERE installation_id=$1`, payload.Installation.ID)
			}
		} else {
			_, err = tx.Exec(r.Context(), `INSERT INTO github_installations(installation_id,organization_id,account_login) VALUES($1,$2,$3) ON CONFLICT(installation_id) DO UPDATE SET account_login=EXCLUDED.account_login`, payload.Installation.ID, s.config.DefaultOrganizationID, payload.Installation.Account.Login)
			if err == nil {
				for _, repository := range payload.Repositories {
					err = enqueueRepositoryScan(r.Context(), tx, s.config.DefaultOrganizationID, payload.Installation.ID, repository.Owner.Login, repository.Name, "resolve:"+delivery)
					if err != nil {
						break
					}
				}
			}
		}
	case "installation_repositories":
		var payload struct {
			Installation struct {
				ID int64 `json:"id"`
			} `json:"installation"`
			RepositoriesAdded []struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repositories_added"`
			RepositoriesRemoved []struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repositories_removed"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.Installation.ID == 0 {
			writeError(w, 400, "invalid_webhook", "Installation repositories payload is malformed")
			return
		}
		var orgID string
		if err = tx.QueryRow(r.Context(), `SELECT organization_id FROM github_installations WHERE installation_id=$1`, payload.Installation.ID).Scan(&orgID); err == nil {
			for _, repository := range payload.RepositoriesAdded {
				err = enqueueRepositoryScan(r.Context(), tx, orgID, payload.Installation.ID, repository.Owner.Login, repository.Name, "resolve:"+delivery)
				if err != nil {
					break
				}
			}
			for _, repository := range payload.RepositoriesRemoved {
				if err == nil {
					err = removeRepositorySource(r.Context(), tx, payload.Installation.ID, repository.Owner.Login, repository.Name)
				}
			}
		}
	case "repository":
		var payload struct {
			Action       string `json:"action"`
			Installation struct {
				ID int64 `json:"id"`
			} `json:"installation"`
			Repository struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repository"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.Installation.ID == 0 || payload.Repository.Name == "" {
			writeError(w, 400, "invalid_webhook", "Repository payload is malformed")
			return
		}
		if payload.Action == "deleted" || payload.Action == "archived" {
			err = removeRepositorySource(r.Context(), tx, payload.Installation.ID, payload.Repository.Owner.Login, payload.Repository.Name)
		} else {
			var orgID string
			if err = tx.QueryRow(r.Context(), `SELECT organization_id FROM github_installations WHERE installation_id=$1`, payload.Installation.ID).Scan(&orgID); err == nil {
				err = enqueueRepositoryScan(r.Context(), tx, orgID, payload.Installation.ID, payload.Repository.Owner.Login, payload.Repository.Name, "resolve:"+delivery)
			}
		}
	case "push":
		var payload struct {
			After        string `json:"after"`
			Deleted      bool   `json:"deleted"`
			Installation struct {
				ID int64 `json:"id"`
			} `json:"installation"`
			Repository struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repository"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.Installation.ID == 0 || payload.Repository.Name == "" {
			writeError(w, 400, "invalid_webhook", "Push payload is malformed")
			return
		}
		if !payload.Deleted && strings.Trim(payload.After, "0") != "" {
			var orgID string
			if err = tx.QueryRow(r.Context(), `SELECT organization_id FROM github_installations WHERE installation_id=$1`, payload.Installation.ID).Scan(&orgID); err == nil {
				err = enqueueRepositoryScan(r.Context(), tx, orgID, payload.Installation.ID, payload.Repository.Owner.Login, payload.Repository.Name, payload.After)
			}
		}
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not apply webhook")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "Could not commit webhook")
		return
	}
	writeJSON(w, 202, map[string]string{"status": "accepted"})
}

func enqueueRepositoryScan(ctx context.Context, tx pgx.Tx, orgID string, installationID int64, owner, repository, commit string) error {
	if owner == "" || repository == "" || commit == "" {
		return fmt.Errorf("repository scan coordinates are incomplete")
	}
	_, err := tx.Exec(ctx, `INSERT INTO repository_scan_jobs(id,organization_id,installation_id,owner,repository,commit_sha,status) VALUES($1,$2,$3,$4,$5,$6,'pending') ON CONFLICT DO NOTHING`, uuid.New(), orgID, installationID, owner, repository, commit)
	return err
}

type githubRepositorySource struct {
	organizationID string
	owner          string
	repository     string
	sourceID       string
}

func removeInstallationSources(ctx context.Context, tx pgx.Tx, installationID int64) error {
	rows, err := tx.Query(ctx, `SELECT organization_id,owner,repository,source_id FROM github_repositories WHERE installation_id=$1`, installationID)
	if err != nil {
		return err
	}
	items := []githubRepositorySource{}
	for rows.Next() {
		var item githubRepositorySource
		if err := rows.Scan(&item.organizationID, &item.owner, &item.repository, &item.sourceID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := removeGitHubSource(ctx, tx, item); err != nil {
			return err
		}
	}
	return nil
}

func removeRepositorySource(ctx context.Context, tx pgx.Tx, installationID int64, owner, repository string) error {
	var item githubRepositorySource
	err := tx.QueryRow(ctx, `SELECT organization_id,owner,repository,source_id FROM github_repositories WHERE installation_id=$1 AND owner=$2 AND repository=$3`, installationID, strings.ToLower(owner), strings.ToLower(repository)).Scan(&item.organizationID, &item.owner, &item.repository, &item.sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return removeGitHubSource(ctx, tx, item)
}

func removeGitHubSource(ctx context.Context, tx pgx.Tx, item githubRepositorySource) error {
	rows, err := tx.Query(ctx, `SELECT entity_id FROM source_entities WHERE organization_id=$1 AND source_id=$2 AND current=true`, item.organizationID, item.sourceID)
	if err != nil {
		return err
	}
	entityIDs := []string{}
	for rows.Next() {
		var entityID string
		if err := rows.Scan(&entityID); err != nil {
			rows.Close()
			return err
		}
		entityIDs = append(entityIDs, entityID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE source_entities SET current=false,stale=true,consecutive_full_misses=3 WHERE organization_id=$1 AND source_id=$2`, item.organizationID, item.sourceID); err != nil {
		return err
	}
	snapshot := discovery.Snapshot{SnapshotID: uuid.NewString(), OrganizationID: item.organizationID, SourceID: item.sourceID}
	for _, entityID := range entityIDs {
		var current bool
		err = tx.QueryRow(ctx, `UPDATE entities e SET current=EXISTS(SELECT 1 FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id AND se.current),stale=NOT EXISTS(SELECT 1 FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id AND se.current AND NOT se.stale) WHERE organization_id=$1 AND id=$2 RETURNING current`, item.organizationID, entityID).Scan(&current)
		if err != nil {
			return err
		}
		if !current {
			if err := recordChange(ctx, tx, snapshot, "entity.removed", entityID); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE source_relationships SET current=false,stale=true,consecutive_full_misses=3 WHERE organization_id=$1 AND source_id=$2`, item.organizationID, item.sourceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE relationships r SET current=EXISTS(SELECT 1 FROM source_relationships sr WHERE sr.organization_id=r.organization_id AND sr.relationship_id=r.id AND sr.current),stale=NOT EXISTS(SELECT 1 FROM source_relationships sr WHERE sr.organization_id=r.organization_id AND sr.relationship_id=r.id AND sr.current AND NOT sr.stale) WHERE r.organization_id=$1`, item.organizationID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sources SET revoked_at=now() WHERE organization_id=$1 AND id=$2`, item.organizationID, item.sourceID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM github_repositories WHERE organization_id=$1 AND source_id=$2`, item.organizationID, item.sourceID)
	return err
}

type RepositoryWorker struct {
	Pool         *pgxpool.Pool
	Client       *githubapp.Client
	Logger       *slog.Logger
	PollInterval time.Duration
}

func (w RepositoryWorker) Run(ctx context.Context) error {
	if w.Client == nil {
		return fmt.Errorf("GitHub App client is required")
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.PollInterval == 0 {
		w.PollInterval = time.Second
	}
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	reconciliation := time.NewTicker(24 * time.Hour)
	defer reconciliation.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := w.processOne(ctx); err != nil {
				w.Logger.Warn("repository scan failed", "error", err)
			}
		case <-reconciliation.C:
			if err := w.enqueueReconciliation(ctx); err != nil {
				w.Logger.Warn("nightly GitHub reconciliation failed", "error", err)
			}
		}
	}
}

func (w RepositoryWorker) enqueueReconciliation(ctx context.Context) error {
	rows, err := w.Pool.Query(ctx, `SELECT installation_id,organization_id FROM github_installations`)
	if err != nil {
		return err
	}
	type installation struct {
		id  int64
		org string
	}
	items := []installation{}
	for rows.Next() {
		var item installation
		if rows.Scan(&item.id, &item.org) == nil {
			items = append(items, item)
		}
	}
	rows.Close()
	for _, item := range items {
		token, _, err := w.Client.InstallationToken(ctx, item.id)
		if err != nil {
			return err
		}
		repositories, err := w.Client.Repositories(ctx, token)
		if err != nil {
			return err
		}
		for _, repository := range repositories {
			commit, err := w.Client.HeadCommit(ctx, token, repository)
			if err != nil {
				w.Logger.Debug("GitHub repository head unavailable", "repository", repository.Owner+"/"+repository.Name, "error", err)
				continue
			}
			_, err = w.Pool.Exec(ctx, `INSERT INTO repository_scan_jobs(id,organization_id,installation_id,owner,repository,commit_sha,status) VALUES($1,$2,$3,$4,$5,$6,'pending') ON CONFLICT DO NOTHING`, uuid.New(), item.org, item.id, repository.Owner, repository.Name, commit)
			if err != nil {
				return err
			}
		}
		if err := w.removeRepositoriesMissingFromReconciliation(ctx, item.id, repositories); err != nil {
			return err
		}
	}
	return nil
}

func (w RepositoryWorker) removeRepositoriesMissingFromReconciliation(ctx context.Context, installationID int64, repositories []githubapp.Repository) error {
	present := map[string]struct{}{}
	for _, repository := range repositories {
		present[strings.ToLower(repository.Owner)+"/"+strings.ToLower(repository.Name)] = struct{}{}
	}
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT organization_id,owner,repository,source_id FROM github_repositories WHERE installation_id=$1`, installationID)
	if err != nil {
		return err
	}
	removed := []githubRepositorySource{}
	for rows.Next() {
		var item githubRepositorySource
		if err := rows.Scan(&item.organizationID, &item.owner, &item.repository, &item.sourceID); err != nil {
			rows.Close()
			return err
		}
		if _, exists := present[item.owner+"/"+item.repository]; !exists {
			removed = append(removed, item)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range removed {
		if err := removeGitHubSource(ctx, tx, item); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (w RepositoryWorker) processOne(ctx context.Context) (bool, error) {
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	var id uuid.UUID
	var orgID, owner, repository, commit string
	var installationID int64
	var attempts int
	err = tx.QueryRow(ctx, `SELECT id,organization_id,installation_id,owner,repository,commit_sha,attempts FROM repository_scan_jobs WHERE status='pending' AND created_at<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &orgID, &installationID, &owner, &repository, &commit, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		tx.Rollback(ctx)
		return false, nil
	}
	if err != nil {
		tx.Rollback(ctx)
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE repository_scan_jobs SET status='processing',attempts=attempts+1 WHERE id=$1`, id); err != nil {
		tx.Rollback(ctx)
		return true, err
	}
	if err = tx.Commit(ctx); err != nil {
		return true, err
	}
	if strings.HasPrefix(commit, "resolve:") {
		token, _, resolveErr := w.Client.InstallationToken(ctx, installationID)
		if resolveErr == nil {
			commit, resolveErr = w.Client.HeadCommit(ctx, token, githubapp.Repository{Owner: owner, Name: repository, DefaultBranch: "HEAD"})
		}
		if resolveErr != nil {
			jobErr := fmt.Errorf("resolve repository head: %w", resolveErr)
			status := "pending"
			if attempts+1 >= 5 {
				status = "failed"
			}
			_, markErr := w.Pool.Exec(ctx, `UPDATE repository_scan_jobs SET status=$2,error_message=$3,created_at=now()+interval '1 minute',completed_at=CASE WHEN $2='failed' THEN now() ELSE NULL END WHERE id=$1`, id, status, safeError(jobErr))
			if markErr != nil {
				return true, fmt.Errorf("resolve: %v; mark: %w", jobErr, markErr)
			}
			return true, jobErr
		}
	}
	jobErr := w.scan(ctx, orgID, installationID, owner, repository, commit)
	if jobErr != nil {
		status := "pending"
		if attempts+1 >= 5 {
			status = "failed"
		}
		_, markErr := w.Pool.Exec(ctx, `UPDATE repository_scan_jobs SET status=$2,error_message=$3,created_at=now()+interval '1 minute',completed_at=CASE WHEN $2='failed' THEN now() ELSE NULL END WHERE id=$1`, id, status, safeError(jobErr))
		if markErr != nil {
			return true, fmt.Errorf("scan: %v; mark: %w", jobErr, markErr)
		}
		return true, jobErr
	}
	_, err = w.Pool.Exec(ctx, `UPDATE repository_scan_jobs SET status='complete',error_message=NULL,completed_at=now() WHERE id=$1`, id)
	return true, err
}
func (w RepositoryWorker) scan(ctx context.Context, orgID string, installationID int64, owner, repository, commit string) error {
	token, _, err := w.Client.InstallationToken(ctx, installationID)
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "lens-repository-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := w.Client.DownloadRepository(ctx, token, owner, repository, commit, temporary); err != nil {
		return err
	}
	repositoryURL := "https://github.com/" + owner + "/" + repository
	sourceID := discovery.StableID(orgID, discovery.KindRepository, repositoryURL)
	_, err = w.Pool.Exec(ctx, `INSERT INTO sources(organization_id,id,source_type,name,last_sequence,last_full_sequence) VALUES($1,$2,'repository',$3,0,0) ON CONFLICT(organization_id,id) DO UPDATE SET name=EXCLUDED.name,revoked_at=NULL`, orgID, sourceID, owner+"/"+repository)
	if err != nil {
		return err
	}
	_, err = w.Pool.Exec(ctx, `INSERT INTO github_repositories(installation_id,organization_id,owner,repository,source_id,last_seen_at) VALUES($1,$2,$3,$4,$5,now()) ON CONFLICT(installation_id,owner,repository) DO UPDATE SET source_id=EXCLUDED.source_id,last_seen_at=now()`, installationID, orgID, strings.ToLower(owner), strings.ToLower(repository), sourceID)
	if err != nil {
		return err
	}
	snapshot, err := repositoryscanner.Scan(ctx, repositoryscanner.Options{OrganizationID: orgID, SourceID: sourceID, Root: temporary, RepositoryURL: repositoryURL, CommitSHA: commit})
	if err != nil {
		return err
	}
	snapshot.Sequence = 0
	snapshot.Full = true
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = w.Pool.Exec(ctx, `INSERT INTO ingestion_jobs(id,organization_id,source_id,snapshot_id,status,payload) VALUES($1,$2,$3,$4,'pending',$5) ON CONFLICT(organization_id,snapshot_id) DO NOTHING`, uuid.New(), orgID, sourceID, snapshot.SnapshotID, payload)
	return err
}
