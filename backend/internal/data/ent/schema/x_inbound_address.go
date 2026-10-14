package schema

// Fork addition (order import). See FORK_NOTES.md.

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// XInboundAddress is the per-collection forwarding address / webhook token for
// order import. group_id is a plain column (FK only in the SQL migrations) so
// the upstream Group schema does not need a new edge.
type XInboundAddress struct {
	ent.Schema
}

func (XInboundAddress) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_inbound_addresses"}}
}

func (XInboundAddress) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.BaseMixin{}}
}

func (XInboundAddress) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("group_id", uuid.UUID{}),
		// token is >=128 bits of randomness, base32-encoded. It is the bearer
		// secret in the webhook URL and the address local part.
		field.String("token").NotEmpty().MaxLen(128).Sensitive(),
		// allowed_senders is a newline/comma separated allow-list of sender
		// addresses or @domains. Empty means any sender is accepted.
		field.Text("allowed_senders").Default(""),
		field.Time("last_received_at").Optional().Nillable(),
	}
}

func (XInboundAddress) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id").Unique(),
		index.Fields("token").Unique(),
	}
}
