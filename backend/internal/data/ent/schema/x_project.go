package schema

// Fork addition (use & build: "What can I build?"). See FORK_NOTES.md.

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

// XProject is a saved build idea. Its parts reserve owned entities.
type XProject struct {
	ent.Schema
}

func (XProject) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_projects"}}
}

func (XProject) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.BaseMixin{}}
}

func (XProject) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("group_id", uuid.UUID{}),
		field.String("name").NotEmpty().MaxLen(255),
		field.Text("description").Default(""),
		field.Enum("build_status").Values("buildable_now", "missing_parts").Default("missing_parts"),
		// steps and missing_parts are JSON arrays of strings / objects.
		field.Text("steps").Default("[]"),
		field.Text("missing_parts").Default("[]"),
	}
}

func (XProject) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("parts", XProjectPart.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (XProject) Indexes() []ent.Index {
	return []ent.Index{index.Fields("group_id")}
}

// XProjectPart reserves a quantity of an owned entity for a project.
type XProjectPart struct {
	ent.Schema
}

func (XProjectPart) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "x_project_parts"}}
}

func (XProjectPart) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.BaseMixin{}}
}

func (XProjectPart) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("project_id", uuid.UUID{}),
		field.UUID("entity_id", uuid.UUID{}),
		field.Float("quantity").Default(1),
		field.String("role").Default("").MaxLen(255),
	}
}

func (XProjectPart) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", XProject.Type).Ref("parts").Field("project_id").Unique().Required(),
	}
}

func (XProjectPart) Indexes() []ent.Index {
	return []ent.Index{index.Fields("entity_id"), index.Fields("project_id", "entity_id").Unique()}
}
