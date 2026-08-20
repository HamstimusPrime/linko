package linkoerr

import (
	"errors"
	"log/slog"
)

type errWithAttrs struct {
	error
	attr []slog.Attr
}

func (e *errWithAttrs) Unwrap() error {
	return e.error
}

func (e *errWithAttrs) Attrs() []slog.Attr {
	return e.attr
}

func Attrs(e error) []slog.Attr {
	var attr []slog.Attr
	//check if error passed satisfies attrErr
	// err = err.Unwrap()
	for e != nil {
		if ae, ok := e.(attrErr); ok {
			attr = append(attr, ae.Attrs()...)
		}
		e = errors.Unwrap(e)
	}
	return attr
}

type attrErr interface {
	Attrs() []slog.Attr
}

func WithAttrs(err error, args ...any) error {
	//function that takes in an error and attributes, and uses it to creates an errWithAttrs object
	return &errWithAttrs{
		error: err,
		attr:  argsToAttr(args),
	}
}

func argsToAttr(args []any) []slog.Attr {
	//create map of slog.Attr types
	attr := make([]slog.Attr, 0, len(args))
	//go through items in args and assert for slog.Attr types
	for i := 0; i < len(args); {
		switch key := args[i].(type) {
		case slog.Attr:
			//if key is an slog.Attr type, append it to the attr map
			attr = append(attr, key)
			i++
		case string:
			if i+1 >= len(args) {
				//if key is a string but is the last item on the list,
				//create an attribute with a key called !!BADKEY and make its value the
				//current item on the args list
				attr = append(attr, slog.String("!BADKEY", key))
				i++
			} else {
				attr = append(attr, slog.Any(key, args[i+1]))
				i += 2
			}
		default:
			attr = append(attr, slog.Any("!BADKEY", args[i]))
			i++

		}
	}

	return attr

}
