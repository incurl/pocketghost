package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"

	"github.com/incurl/pocketghost/internal/app"
)

// fileAuth authenticates FTP/SFTP logins against the metadata database and maps
// each user to a jailed view of their own instance directories.
type fileAuth struct {
	app *app.App
}

func newFileAuth(a *app.App) *fileAuth { return &fileAuth{app: a} }

// userRoot ensures `<DATA_ROOT>/users/<uid>` exists and returns it. The directory
// contains one symlink per owned instance so a tenant only sees its own files.
func (f *fileAuth) userRoot(ctx context.Context, uid string) (string, error) {
	root := filepath.Join(f.app.Config.DataRoot, "users", uid)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	instances, err := f.app.Repo.ListInstances(ctx, uid)
	if err != nil {
		return "", err
	}
	for _, inst := range instances {
		link := filepath.Join(root, inst.Subdomain)
		if _, err := os.Lstat(link); err == nil {
			continue
		}
		src := f.app.Config.InstanceDataPath(inst.ID)
		if err := os.Symlink(src, link); err != nil {
			// Fall back to a real directory when symlinks are unavailable.
			_ = os.MkdirAll(link, 0o755)
		}
	}
	return root, nil
}

// passwordLogin verifies an email/password pair.
func (f *fileAuth) passwordLogin(user, pass string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), f.app.Config.StopTimeout)
	defer cancel()

	u, err := f.app.Repo.GetUserByEmail(ctx, user)
	if err != nil {
		return "", false
	}
	if u.Suspension != "" {
		return "", false
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(pass)) != nil {
		return "", false
	}
	root, err := f.userRoot(context.Background(), u.ID)
	if err != nil {
		return "", false
	}
	return root, true
}

// Login implements ftp.Auth.
func (f *fileAuth) Login(user, pass string) (string, bool) { return f.passwordLogin(user, pass) }

// Password implements sftp.Auth.
func (f *fileAuth) Password(user, pass string) (string, bool) { return f.passwordLogin(user, pass) }

// PublicKey implements sftp.Auth, matching a key fingerprint to `ssh_keys`.
func (f *fileAuth) PublicKey(user string, key ssh.PublicKey) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), f.app.Config.StopTimeout)
	defer cancel()

	fp := ssh.FingerprintSHA256(key)
	owner, err := f.app.Repo.GetUserByEmail(ctx, user)
	if err != nil {
		return "", false
	}
	keys, err := f.app.Repo.ListSSHKeys(ctx, owner.ID)
	if err != nil {
		return "", false
	}
	for _, k := range keys {
		stored, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.Key))
		if err != nil {
			continue
		}
		if strings.EqualFold(ssh.FingerprintSHA256(stored), fp) {
			root, rerr := f.userRoot(context.Background(), owner.ID)
			if rerr != nil {
				return "", false
			}
			return root, true
		}
	}
	return "", false
}

var _ = fmt.Sprintf
