package ai

import (
	"bytes"
	"encoding/json"
)

// object is a JSON object that keeps its key order, standing in for the
// object literals pi builds (JavaScript objects serialize in insertion
// order). Request params stay Go maps; the objects inside them use this.
type object []field

type field struct {
	Key   string
	Value any
}

// obj builds an object from key, value pairs.
func obj(kv ...any) object {
	o := make(object, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		o = append(o, field{kv[i].(string), kv[i+1]})
	}
	return o
}

// get returns the value for key.
func (o object) get(key string) (any, bool) {
	for _, f := range o {
		if f.Key == key {
			return f.Value, true
		}
	}
	return nil, false
}

// set replaces key's value or appends it.
func (o *object) set(key string, value any) {
	for i := range *o {
		if (*o)[i].Key == key {
			(*o)[i].Value = value
			return
		}
	}
	*o = append(*o, field{key, value})
}

// omit is a value that drops its key, like undefined in JavaScript.
type omitValue struct{}

var undefined = omitValue{}

func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, f := range o {
		if _, skip := f.Value.(omitValue); skip {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		k, err := json.Marshal(f.Key)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		v, err := json.Marshal(f.Value)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
