package jsonpath

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

type (
	// pathTokenType represents single JSONPath token.
	pathTokenType byte

	// pathParser combines JSONPath and a position to start parsing from.
	pathParser struct {
		s string
		i int
	}
)

const (
	pathRoot pathTokenType = iota
	pathDot
	pathLeftBracket
	pathRightBracket
	pathAsterisk
	pathComma
	pathColon
	pathIdentifier
	pathString
	pathNumber
)

// Get returns substructures of value selected by path.
// The result is always non-nil unless path is invalid.
func Get(path string, value interface{}) ([]interface{}, bool) {
	p := pathParser{s: path}

	typ, _, ok := p.nextToken()
	if !ok {
		return nil, false
	}

	if typ != pathRoot {
		return nil, false
	}

	objs := []interface{}{value}
	for p.i < len(p.s) {
		typ, _, ok := p.nextToken()
		if !ok {
			return nil, false
		}

		switch typ {
		case pathDot:
			objs, ok = p.processDot(objs)
		case pathLeftBracket:
			objs, ok = p.processLeftBracket(objs)
		default:
			return nil, false
		}

		if !ok {
			return nil, false
		}
	}

	if objs == nil {
		objs = []interface{}{}
	}
	return objs, true
}

func (p *pathParser) nextToken() (pathTokenType, string, bool) {
	var (
		typ     pathTokenType
		value   string
		ok      = true
		numRead = 1
	)

	switch c := p.s[p.i]; c {
	case '$':
		typ = pathRoot
	case '.':
		typ = pathDot
	case '[':
		typ = pathLeftBracket
	case ']':
		typ = pathRightBracket
	case '*':
		typ = pathAsterisk
	case ',':
		typ = pathComma
	case ':':
		typ = pathColon
	case '\'':
		typ = pathString
		value, numRead, ok = p.parseString()
	default:
		switch {
		case c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z'):
			typ = pathIdentifier
			value, numRead, ok = p.parseIdent()
		case c == '-' || ('0' <= c && c <= '9'):
			typ = pathNumber
			value, numRead, ok = p.parseNumber()
		default:
			return 0, "", false
		}
	}

	p.i += numRead
	return typ, value, ok
}

// parseString parses JSON string surrounded by single quotes.
// It returns number of characters were consumed and true on success.
func (p *pathParser) parseString() (string, int, bool) {
	var end int
	for end = p.i + 1; end < len(p.s); end++ {
		if p.s[end] == '\'' {
			return p.s[p.i : end+1], end + 1 - p.i, true
		}
	}

	return "", 0, false
}

// parseIdent parses alphanumeric identifier.
// It returns number of characters were consumed and true on success.
func (p *pathParser) parseIdent() (string, int, bool) {
	var end int
	for end = p.i + 1; end < len(p.s); end++ {
		c := p.s[end]
		if c != '_' && !('a' <= c && c <= 'z') &&
			!('A' <= c && c <= 'Z') && !('0' <= c && c <= '9') {
			break
		}
	}

	return p.s[p.i:end], end - p.i, true
}

// parseNumber parses integer number.
// Only string representation is returned, size-checking is done on the first use.
// It also returns number of characters were consumed and true on success.
func (p *pathParser) parseNumber() (string, int, bool) {
	var end int
	for end = p.i + 1; end < len(p.s); end++ {
		c := p.s[end]
		if c < '0' || '9' < c {
			break
		}
	}

	return p.s[p.i:end], end - p.i, true
}

// processDot handles `.` operator.
// It either descends 1 level down or performs recursive descent.
func (p *pathParser) processDot(objs []interface{}) ([]interface{}, bool) {
	typ, value, ok := p.nextToken()
	if !ok {
		return nil, false
	}

	switch typ {
	case pathAsterisk:
		return p.descent(objs)
	case pathDot:
		return p.recursiveDescent(objs)
	case pathIdentifier:
		return p.descentIdent(objs, value)
	default:
		return nil, false
	}
}

// descent descends 1 level down.
// It flattens arrays and returns map values for maps.
func (p *pathParser) descent(objs []interface{}) ([]interface{}, bool) {
	var values []interface{}
	for i := range objs {
		switch obj := objs[i].(type) {
		case []interface{}:
			values = append(values, obj...)
		case map[string]interface{}:
			keys := make([]string, 0, len(obj))
			for k := range obj {
				keys = append(keys, k)
			}

			sort.Strings(keys)

			for _, k := range keys {
				values = append(values, obj[k])
			}
		}
	}

	return values, true
}

// recursiveDescent performs recursive descent.
func (p *pathParser) recursiveDescent(objs []interface{}) ([]interface{}, bool) {
	typ, val, ok := p.nextToken()
	if !ok {
		return nil, false
	}

	if typ != pathAsterisk && typ != pathIdentifier {
		return nil, false
	}

	var values []interface{}

	for len(objs) > 0 {
		if typ == pathAsterisk {
			objs, _ = p.descent(objs)
			values = append(values, objs...)
		} else {
			newObjs, _ := p.descentIdent(objs, val)
			values = append(values, newObjs...)
			objs, _ = p.descent(objs)
		}
	}

	return values, true
}

// descentIdent performs map's field access by name.
func (p *pathParser) descentIdent(objs []interface{}, names ...string) ([]interface{}, bool) {
	var values []interface{}
	for i := range objs {
		obj, ok := objs[i].(map[string]interface{})
		if !ok {
			continue
		}

		for j := range names {
			if v, ok := obj[names[j]]; ok {
				values = append(values, v)
			}
		}
	}
	return values, true
}

// descentIndex performs array access by index.
func (p *pathParser) descentIndex(objs []interface{}, indices ...int) ([]interface{}, bool) {
	var values []interface{}
	for i := range objs {
		obj, ok := objs[i].([]interface{})
		if !ok {
			continue
		}

		for _, j := range indices {
			if j < len(obj) {
				values = append(values, obj[j])
			}
		}
	}

	return values, true
}

// processLeftBracket processes index expressions which can be either
// array/map access, array sub-slice or union of indices.
func (p *pathParser) processLeftBracket(objs []interface{}) ([]interface{}, bool) {
	typ, value, ok := p.nextToken()
	if !ok {
		return nil, ok
	}

	switch typ {
	case pathAsterisk:
		typ, _, ok := p.nextToken()
		if !ok || typ != pathRightBracket {
			return nil, false
		}

		return p.descent(objs)
	case pathColon:
		return p.processSlice(objs, 0)
	case pathNumber:
		subTyp, _, ok := p.nextToken()
		if !ok {
			return nil, false
		}

		switch subTyp {
		case pathColon:
			index, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				return nil, false
			}

			return p.processSlice(objs, int(index))
		case pathComma:
			return p.processUnion(objs, pathNumber, value)
		case pathRightBracket:
			index, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				return nil, false
			}

			return p.descentIndex(objs, int(index))
		default:
			return nil, false
		}
	case pathString:
		subTyp, _, ok := p.nextToken()
		if !ok {
			return nil, false
		}

		switch subTyp {
		case pathComma:
			return p.processUnion(objs, pathString, value)
		case pathRightBracket:
			s := strings.Trim(value, "'")
			err := json.Unmarshal([]byte(`"`+s+`"`), &s)
			if err != nil {
				return nil, false
			}
			return p.descentIdent(objs, s)
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

// processUnion processes union of multiple indices.
// firstTyp is assumed to be either pathNumber or pathString.
func (p *pathParser) processUnion(objs []interface{}, firstTyp pathTokenType, firstVal string) ([]interface{}, bool) {
	items := []string{firstVal}
	for {
		typ, val, ok := p.nextToken()
		if !ok || typ != firstTyp {
			return nil, false
		}

		items = append(items, val)
		typ, val, ok = p.nextToken()
		if !ok {
			return nil, false
		} else if typ == pathRightBracket {
			break
		} else if typ != pathComma {
			return nil, false
		}
	}

	switch firstTyp {
	case pathNumber:
		values := make([]int, len(items))
		for i := range items {
			index, err := strconv.ParseInt(items[i], 10, 32)
			if err != nil {
				return nil, false
			}
			values[i] = int(index)
		}

		return p.descentIndex(objs, values...)
	case pathString:
		for i := range items {
			s := strings.Trim(items[i], "'")
			err := json.Unmarshal([]byte(`"`+s+`"`), &items[i])
			if err != nil {
				return nil, false
			}
		}
		return p.descentIdent(objs, items...)
	default:
		panic("token in union must be either number or string")
	}
}

// processSlice processes slice with the specified start index.
func (p *pathParser) processSlice(objs []interface{}, start int) ([]interface{}, bool) {
	typ, val, ok := p.nextToken()
	if !ok {
		return nil, false
	}

	switch typ {
	case pathNumber:
		typ, _, ok := p.nextToken()
		if !ok || typ != pathRightBracket {
			return nil, false
		}

		index, err := strconv.ParseInt(val, 10, 32)
		if err != nil {
			return nil, false
		}

		return p.descentRange(objs, start, int(index))
	case pathRightBracket:
		return p.descentRange(objs, start, 0)
	default:
		return nil, false
	}
}

// descentRange is similar to descent but skips maps and returns sub-slices for arrays.
func (p *pathParser) descentRange(objs []interface{}, start, end int) ([]interface{}, bool) {
	var values []interface{}
	for i := range objs {
		arr, ok := objs[i].([]interface{})
		if !ok {
			continue
		}

		subStart := start
		if subStart < 0 {
			subStart += len(arr)
		}

		subEnd := end
		if subEnd <= 0 {
			subEnd += len(arr)
		}

		if subEnd > len(arr) {
			subEnd = len(arr)
		}

		count := subEnd - subStart
		if count < 0 {
			return nil, false
		}
		values = append(values, arr[subStart:subEnd]...)
	}

	return values, true
}
