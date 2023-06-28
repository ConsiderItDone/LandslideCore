package query_test

import (
	"testing"

	"github.com/consideritdone/landslidecore/libs/pubsub/query"
	"github.com/stretchr/testify/assert"
)

func TestEmptyQueryMatchesAnything(t *testing.T) {
	q := query.Empty{}

	testCases := []struct {
		query map[string][]string
	}{
		{map[string][]string{}},
		{map[string][]string{"Asher": {"Roth"}}},
		{map[string][]string{"Route": {"66"}}},
		{map[string][]string{"Route": {"66"}, "Billy": {"Blue"}}},
	}

	for _, tc := range testCases {
		match, err := q.Matches(tc.query)
		assert.Nil(t, err)
		assert.True(t, match)
	}
}
