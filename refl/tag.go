package refl

import "reflect"

// HasTaggedFields checks if the structure has fields with tag name
func HasTaggedFields(i interface{}, tagname string) bool {
	if i == nil {
		return false
	}
	t := reflect.TypeOf(i)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get(tagname)
		if tag != "" && tag != "-" {
			return true
		}
		if field.Anonymous {
			if tag != "-" && HasTaggedFields(reflect.New(field.Type).Interface(), tagname) {
				return true
			}
		}

	}
	return false
}
