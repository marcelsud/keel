package core

import (
	"errors"
	"fmt"

	"github.com/marcelsud/keel/internal/protocol"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

const AdminUser = "keel-admin"

// Credentials contains local deployment secrets. Keep this file outside version control.
type Credentials struct {
	Admin    string            `json:"admin"`
	Services map[string]string `json:"services"`
}

func configureAuth(options *server.Options, credentials *Credentials) error {
	if credentials == nil {
		return nil
	}
	if len(credentials.Admin) < 24 {
		return errors.New("administrator credential must contain at least 24 characters")
	}
	options.Authorization = ""
	options.Users = []*server.User{{Username: AdminUser, Password: credentials.Admin}}
	seen := map[string]bool{credentials.Admin: true}
	for name, secret := range credentials.Services {
		if !protocol.ValidName(name) || name == AdminUser || name == "keel-core" || len(secret) < 24 || seen[secret] {
			return fmt.Errorf("invalid or reused credential for service %q", name)
		}
		seen[secret] = true
		// Dynamic response grants can authorize arbitrary SQL subjects supplied as
		// Reply fields. Replies instead have an explicit, inbox-only publish rule.
		options.Users = append(options.Users, &server.User{Username: name, Password: secret, Permissions: &server.Permissions{
			Publish: &server.SubjectPermission{Allow: []string{
				protocol.Prefix + ".core.>", protocol.Prefix + ".actions.>", protocol.Prefix + ".rpc.>", protocol.Prefix + ".schema.>",
				protocol.SQLSubject(name, "query"), protocol.SQLSubject(name, "begin"), protocol.SQLSubject(name, "tx.*.*"),
				"$SRV.>",
				"keel.inbox.>",
				"$JS.API.CONSUMER.INFO.KEEL_EVENTS.*", "$JS.API.CONSUMER.INFO.KEEL_JOBS.*",
				"$JS.API.CONSUMER.MSG.NEXT.KEEL_EVENTS.*", "$JS.API.CONSUMER.MSG.NEXT.KEEL_JOBS.*",
				"$JS.API.STREAM.INFO.KEEL_EVENTS", "$JS.API.STREAM.INFO.KEEL_JOBS",
				"$JS.ACK.KEEL_EVENTS.>", "$JS.ACK.KEEL_JOBS.>",
			}},
			Subscribe: &server.SubjectPermission{Allow: []string{
				"keel.inbox." + name + ".>", protocol.Prefix + ".actions." + name + ".>", protocol.Prefix + ".rpc.>", protocol.Prefix + ".schema.>", "$SRV.>",
			}},
		}})
	}
	return nil
}

func coreAuth(options Options) nats.Option {
	if options.Credentials != nil {
		return nats.UserInfo(AdminUser, options.Credentials.Admin)
	}
	return nats.Token(options.Token)
}
