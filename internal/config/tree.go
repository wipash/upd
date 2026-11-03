package config

import (
	"errors"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Position is a 1-based line and column in a config file.
type Position struct{ Line, Col int }

// value is a TOML value annotated with where it appears in the file. The
// go-toml decoder loses positions for anything it reports through custom
// unmarshalers, so the config is decoded from this tree instead.
type value struct {
	kind   unstable.Kind
	pos    Position
	text   string
	items  []*value
	fields []*field
}

type field struct {
	name   string
	keyPos Position
	val    *value
}

func (v *value) get(name string) *field {
	for _, f := range v.fields {
		if f.name == name {
			return f
		}
	}
	return nil
}

type treeBuilder struct{ p unstable.Parser }

func (b *treeBuilder) pos(r unstable.Range) Position {
	s := b.p.Shape(r).Start
	return Position{s.Line, s.Column}
}

// parseTree checks the document with go-toml's own decoder first, which
// reports syntax errors, duplicate keys and table redefinitions with good
// messages, so the tree builder can assume a well-formed document.
func parseTree(file string, doc []byte) (*value, error) {
	var sink any
	if err := toml.Unmarshal(doc, &sink); err != nil {
		var de *toml.DecodeError
		if errors.As(err, &de) {
			row, col := de.Position()
			return nil, &Error{File: file, Pos: Position{row, col}, Msg: de.Error()}
		}
		return nil, &Error{File: file, Msg: err.Error()}
	}
	b := &treeBuilder{}
	b.p.Reset(doc)
	root := &value{kind: unstable.Table, pos: Position{1, 1}}
	cur := root
	for b.p.NextExpression() {
		e := b.p.Expression()
		switch e.Kind {
		case unstable.Table:
			cur = b.descend(root, keyParts(e))
		case unstable.ArrayTable:
			parts := keyParts(e)
			return nil, &Error{File: file, Pos: b.pos(parts[0].Raw), Msg: "arrays of tables ([[...]]) are not used in this config"}
		case unstable.KeyValue:
			b.keyValue(cur, e)
		}
	}
	if err := b.p.Error(); err != nil {
		return nil, &Error{File: file, Msg: err.Error()}
	}
	return root, nil
}

func keyParts(n *unstable.Node) []*unstable.Node {
	var parts []*unstable.Node
	it := n.Key()
	for it.Next() {
		parts = append(parts, it.Node())
	}
	return parts
}

func (b *treeBuilder) descend(t *value, keys []*unstable.Node) *value {
	for _, k := range keys {
		f := t.get(string(k.Data))
		if f == nil {
			kp := b.pos(k.Raw)
			f = &field{name: string(k.Data), keyPos: kp, val: &value{kind: unstable.Table, pos: kp}}
			t.fields = append(t.fields, f)
		}
		t = f.val
	}
	return t
}

func (b *treeBuilder) keyValue(t *value, kv *unstable.Node) {
	parts := keyParts(kv)
	t = b.descend(t, parts[:len(parts)-1])
	last := parts[len(parts)-1]
	kp := b.pos(last.Raw)
	t.fields = append(t.fields, &field{name: string(last.Data), keyPos: kp, val: b.value(kv.Value(), kp)})
}

// value converts a parser node. Arrays and inline tables have an empty Raw
// range, so they take the position of their key.
func (b *treeBuilder) value(n *unstable.Node, fallback Position) *value {
	p := fallback
	if n.Raw.Length > 0 {
		p = b.pos(n.Raw)
	}
	switch n.Kind {
	case unstable.Array:
		v := &value{kind: unstable.Array, pos: p}
		it := n.Children()
		for it.Next() {
			v.items = append(v.items, b.value(it.Node(), p))
		}
		return v
	case unstable.InlineTable:
		v := &value{kind: unstable.Table, pos: p}
		it := n.Children()
		for it.Next() {
			b.keyValue(v, it.Node())
		}
		return v
	default:
		return &value{kind: n.Kind, pos: p, text: string(n.Data)}
	}
}
