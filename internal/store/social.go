package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/enowdev/antares/internal/secret"
)

const socialAccountCols = `id,platform,display_name,username,encrypted_password,encrypted_recovery,profile_url,status,rag_namespace,skill_name,last_checked_at,created_at,updated_at`

// socialBox returns the social master-key Box. Social encryption is opt-in;
// if the key is not configured, social credential storage is unavailable.
func (s *sqlStore) socialBox() (*secret.Box, error) {
	s.socialBoxOnce.Do(func() {
		key, err := secret.SocialDefault()
		if err != nil {
			s.socialKeyErr = err
			return
		}
		s.socialKeyBox, s.socialKeyErr = key.Box()
	})
	return s.socialKeyBox, s.socialKeyErr
}

// PutSocialAccount inserts or updates a social account, encrypting secrets.
func (s *sqlStore) PutSocialAccount(ctx context.Context, a *SocialAccount) error {
	box, err := s.socialBox()
	if err != nil {
		return err
	}
	now := time.Now()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now

	encPass, err := box.Encrypt(a.Password)
	if err != nil {
		return err
	}
	encRecovery, err := box.Encrypt(a.RecoveryCodes)
	if err != nil {
		return err
	}

	var lastChecked any
	if a.LastCheckedAt != nil {
		lastChecked = ms(*a.LastCheckedAt)
	}

	_, err = s.exec(ctx, `INSERT INTO social_accounts (`+socialAccountCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			platform=excluded.platform,
			display_name=excluded.display_name,
			username=excluded.username,
			encrypted_password=excluded.encrypted_password,
			encrypted_recovery=excluded.encrypted_recovery,
			profile_url=excluded.profile_url,
			status=excluded.status,
			rag_namespace=excluded.rag_namespace,
			skill_name=excluded.skill_name,
			last_checked_at=excluded.last_checked_at,
			updated_at=excluded.updated_at`,
		a.ID, a.Platform, a.DisplayName, a.Username, encPass, encRecovery,
		a.ProfileURL, a.Status, a.RAGNamespace, a.SkillName, lastChecked,
		ms(a.CreatedAt), ms(a.UpdatedAt))
	return err
}

// GetSocialAccount retrieves one account by ID, decrypting secrets.
func (s *sqlStore) GetSocialAccount(ctx context.Context, id string) (*SocialAccount, error) {
	row := s.row(ctx, `SELECT `+socialAccountCols+` FROM social_accounts WHERE id = ?`, id)
	a, err := scanSocialAccount(row)
	if err != nil {
		return nil, err
	}
	if err := s.decryptSocialAccount(&a); err != nil {
		return nil, err
	}
	return &a, nil
}

// ListSocialAccounts returns all social accounts, decrypting secrets.
func (s *sqlStore) ListSocialAccounts(ctx context.Context) ([]SocialAccount, error) {
	rows, err := s.query(ctx, `SELECT `+socialAccountCols+` FROM social_accounts ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SocialAccount
	for rows.Next() {
		a, err := scanSocialAccount(rows)
		if err != nil {
			return nil, err
		}
		if err := s.decryptSocialAccount(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteSocialAccount removes an account and its encrypted credentials.
func (s *sqlStore) DeleteSocialAccount(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `DELETE FROM social_accounts WHERE id = ?`, id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSocialAccount(sc scanner) (SocialAccount, error) {
	var a SocialAccount
	var encPass, encRecovery string
	var lastChecked sql.NullInt64
	var created, updated int64
	err := sc.Scan(
		&a.ID, &a.Platform, &a.DisplayName, &a.Username,
		&encPass, &encRecovery, &a.ProfileURL, &a.Status,
		&a.RAGNamespace, &a.SkillName, &lastChecked,
		&created, &updated,
	)
	if err != nil {
		return a, err
	}
	a.CreatedAt = fromMS(created)
	a.UpdatedAt = fromMS(updated)
	if lastChecked.Valid {
		t := fromMS(lastChecked.Int64)
		a.LastCheckedAt = &t
	}
	// Store encrypted values temporarily for deferred decrypt.
	a.Password = encPass
	a.RecoveryCodes = encRecovery
	return a, nil
}

func (s *sqlStore) decryptSocialAccount(a *SocialAccount) error {
	box, err := s.socialBox()
	if err != nil {
		return err
	}
	pass, err := box.Decrypt(a.Password)
	if err != nil {
		return err
	}
	recovery, err := box.Decrypt(a.RecoveryCodes)
	if err != nil {
		return err
	}
	a.Password = pass
	a.RecoveryCodes = recovery
	return nil
}
