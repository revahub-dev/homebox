package schema

// Fork addition (order import). See FORK_NOTES.md.

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/schema/mixins"
)

// XInboundMessage is one raw import (an email from the webhook or an .eml
// upload, or a receipt/screenshot photo) together with dedup keys and the
// parse outcome.
type XInboundMessage struct {
	ent.Schema
}

func (XInboundMessage) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_inbound_messages"}}
}

func (XInboundMessage) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.BaseMixin{}}
}

func (XInboundMessage) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("group_id", uuid.UUID{}),
		field.Enum("channel").Values("forward", "eml", "photo"),
		field.Enum("status").Values("parsed", "unparsed", "duplicate", "failed").Default("unparsed"),
		// Dedup keys. message_id is the RFC 5322 Message-ID (emails only);
		// content_hash is sha256 of the raw bytes.
		field.String("message_id").Default("").MaxLen(998),
		field.String("content_hash").NotEmpty().MaxLen(64),
		field.String("store").Default("").MaxLen(32),
		field.String("order_number").Default("").MaxLen(128),
		field.Time("order_date").Optional().Nillable(),
		field.Float("order_total").Default(0),
		field.String("sender").Default("").MaxLen(320),
		field.String("subject").Default("").MaxLen(998),
		field.String("mime_type").Default("").MaxLen(128),
		field.String("extractor").Default("").MaxLen(64),
		field.String("error").Default("").MaxLen(1000),
		// raw holds the original bytes; purge is a follow-up task.
		field.Bytes("raw").Optional(),
	}
}

func (XInboundMessage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("candidates", XImportCandidate.Type).
			Annotations(entsql.Annotation{OnDelete: entsql.Cascade}),
	}
}

func (XInboundMessage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id", "content_hash").Unique(),
		index.Fields("group_id", "message_id"),
		index.Fields("group_id", "store", "order_number"),
	}
}
