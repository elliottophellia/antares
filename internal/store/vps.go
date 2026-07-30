package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/enowdev/antares/internal/secret"
)

const vpsCols = `id,label,host,port,username,auth_method,password,private_key,passphrase,created_at,updated_at`

// crypter returns the process secret box, obtained once. VPS credentials are
// unusable without it, so a failure here fails the operation rather than
// silently storing plaintext.
func (s *sqlStore) crypter() (*secret.Box, error) {
	s.boxOnce.Do(func() { s.box, s.boxErr = secret.Default() })
	return s.box, s.boxErr
}

// PutVPSHost inserts or updates a host, encrypting its secret fields at rest.
func (s *sqlStore) PutVPSHost(ctx context.Context, v *VPSHost) error {
	box, err := s.crypter()
	if err != nil {
		return err
	}
	now := time.Now()
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	if v.Port == 0 {
		v.Port = 22
	}

	encPass, err := box.Encrypt(v.Password)
	if err != nil {
		return err
	}
	encKey, err := box.Encrypt(v.PrivateKey)
	if err != nil {
		return err
	}
	encPhrase, err := box.Encrypt(v.Passphrase)
	if err != nil {
		return err
	}

	_, err = s.exec(ctx, `INSERT INTO vps_hosts (`+vpsCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`+
		onConflict("id", `label=EXCLUDED.label, host=EXCLUDED.host, port=EXCLUDED.port,
			username=EXCLUDED.username, auth_method=EXCLUDED.auth_method, password=EXCLUDED.password,
			private_key=EXCLUDED.private_key, passphrase=EXCLUDED.passphrase, updated_at=EXCLUDED.updated_at`),
		v.ID, v.Label, v.Host, v.Port, v.Username, v.AuthMethod,
		encPass, encKey, encPhrase, ms(v.CreatedAt), ms(v.UpdatedAt))
	return err
}

// GetVPSHost returns a host with its secret fields decrypted.
func (s *sqlStore) GetVPSHost(ctx context.Context, id string) (*VPSHost, error) {
	box, err := s.crypter()
	if err != nil {
		return nil, err
	}
	v, err := scanVPSHost(s.row(ctx, `SELECT `+vpsCols+` FROM vps_hosts WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decryptVPS(box, v)
}

// ListVPSHosts returns every host with secrets decrypted, newest first.
func (s *sqlStore) ListVPSHosts(ctx context.Context) ([]VPSHost, error) {
	box, err := s.crypter()
	if err != nil {
		return nil, err
	}
	rows, err := s.query(ctx, `SELECT `+vpsCols+` FROM vps_hosts ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VPSHost{}
	for rows.Next() {
		v, err := scanVPSHost(rows)
		if err != nil {
			return nil, err
		}
		if _, err := decryptVPS(box, v); err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (s *sqlStore) DeleteVPSHost(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `DELETE FROM vps_hosts WHERE id=?`, id)
	return err
}

func scanVPSHost(sc interface{ Scan(...any) error }) (*VPSHost, error) {
	var v VPSHost
	var created, updated int64
	if err := sc.Scan(&v.ID, &v.Label, &v.Host, &v.Port, &v.Username, &v.AuthMethod,
		&v.Password, &v.PrivateKey, &v.Passphrase, &created, &updated); err != nil {
		return nil, err
	}
	v.CreatedAt = fromMS(created)
	v.UpdatedAt = fromMS(updated)
	return &v, nil
}

// decryptVPS turns the stored ciphertext back into usable secrets in place.
func decryptVPS(box *secret.Box, v *VPSHost) (*VPSHost, error) {
	var err error
	if v.Password, err = box.Decrypt(v.Password); err != nil {
		return nil, err
	}
	if v.PrivateKey, err = box.Decrypt(v.PrivateKey); err != nil {
		return nil, err
	}
	if v.Passphrase, err = box.Decrypt(v.Passphrase); err != nil {
		return nil, err
	}
	return v, nil
}
