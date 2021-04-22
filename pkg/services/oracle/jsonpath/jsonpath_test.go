package jsonpath

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

type pathTestCase struct {
	path   string
	result string
}

func unmarshalGet(t *testing.T, js string, path string) ([]interface{}, bool) {
	var v interface{}
	require.NoError(t, json.Unmarshal([]byte(js), &v))
	return Get(path, v)
}

func (p *pathTestCase) testUnmarshalGet(t *testing.T, js string) {
	res, ok := unmarshalGet(t, js, p.path)
	require.True(t, ok)

	data, err := json.Marshal(res)
	require.NoError(t, err)
	require.JSONEq(t, p.result, string(data))
}

func TestInvalidPaths(t *testing.T) {
	bigNum := strconv.FormatInt(int64(math.MaxInt32)+1, 10)

	// errCases contains invalid json path expressions.
	// These are either invalid(&) or unexpected token in some positions
	// or big number/invalid string.
	errCases := []string{
		".",
		"$1",
		"&",
		"$&",
		"$.&",
		"$.[0]",
		"$..&",
		"$..1",
		"$[&]",
		"$[**]",
		"$[1&]",
		"$[" + bigNum + "]",
		"$[" + bigNum + ":]",
		"$[:" + bigNum + "]",
		"$[1," + bigNum + "]",
		"$[" + bigNum + "[]]",
		"$['a'&]",
		"$['a'1]",
		"$['a",
		"$['\\u123']",
		"$['s','\\u123']",
		"$[[]]",
		"$[1,'a']",
		"$[1,1&",
		"$[1,1[]]",
		"$[1:&]",
		"$[1:1[]]",
		"$[1:[]]",
		"$[1:[]]",
	}

	for _, tc := range errCases {
		t.Run(tc, func(t *testing.T) {
			_, ok := unmarshalGet(t, "{}", tc)
			require.False(t, ok)
		})
	}

	t.Run("$[2:1], invalid slice", func(t *testing.T) {
		_, ok := unmarshalGet(t, "[1,2,3]", "$[2:1]")
		require.False(t, ok)
	})
}

func TestDescentIdent(t *testing.T) {
	js := `{
		"store": {
            "name": "big",
            "sub": [ { "name": "sub1" },
			         { "name": "sub2" }
              ],
            "partner": { "name": "ppp" }
		},
		"another": { "name": "small" }
	}`

	testCases := []pathTestCase{
		{"$.store.name", `["big"]`},
		{"$['store']['name']", `["big"]`},
		{"$[*].name", `["small","big"]`},
		{"$.*.name", `["small","big"]`},
		{"$..store.name", `["big"]`},
		{"$.store..name", `["big","ppp","sub1","sub2"]`},
		{"$..sub[*].name", `["sub1","sub2"]`},
		{"$.store..*.name", `["ppp","sub1","sub2"]`},
		{"$..sub.name", `[]`},
		{"$..sub..name", `["sub1","sub2"]`},
	}
	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			tc.testUnmarshalGet(t, js)
		})
	}
}

func TestDescentIndex(t *testing.T) {
	js := `["a","b","c","d"]`

	testCases := []pathTestCase{
		{"$[0]", `["a"]`},
		{"$[3]", `["d"]`},
		{"$[1:2]", `["b"]`},
		{"$[1:-1]", `["b","c"]`},
		{"$[-3:-1]", `["b","c"]`},
		{"$[-3:3]", `["b","c"]`},
		{"$[:3]", `["a","b","c"]`},
		{"$[:100]", `["a","b","c","d"]`},
		{"$[1:]", `["b","c","d"]`},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			tc.testUnmarshalGet(t, js)
		})
	}

	t.Run("$[:][1], skip wrong types", func(t *testing.T) {
		js := `[[1,2],{"1":"4"},[5,6]]`
		p := pathTestCase{"$[:][1]", "[2,6]"}
		p.testUnmarshalGet(t, js)
	})

	t.Run("$[*].*, flatten", func(t *testing.T) {
		js := `[[1,2],{"1":"4"},[5,6]]`
		p := pathTestCase{"$[*].*", "[1,2,\"4\",5,6]"}
		p.testUnmarshalGet(t, js)
	})

	t.Run("$[*].[1:], skip wrong types", func(t *testing.T) {
		js := `[[1,2],3,{"1":"4"},[5,6]]`
		p := pathTestCase{"$[*][1:]", "[2,6]"}
		p.testUnmarshalGet(t, js)
	})
}

func TestUnion(t *testing.T) {
	js := `["a",{"x":1,"y":2,"z":3},"c","d"]`

	testCases := []pathTestCase{
		{"$[0,2]", `["a","c"]`},
		{"$[1]['x','z']", `[1,3]`},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			tc.testUnmarshalGet(t, js)
		})
	}
}
