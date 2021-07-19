package collectorv2

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSendEvents(t *testing.T) {
	c := mockClient("json")
	c.bulkMaxSizeBytes = 10 // must large than sing

	ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	cq, err := newRequest(ts.URL, "", "GET", nil, nil)
	assert.Nil(t, err)
	c.containerReq = cq

	// normal
	em := newEventMocker(withNumber(10), withMessage("abc"))
	rest, err := c.sendEvents(em.events, false)
	assert.Nil(t, err)
	assert.Empty(t, rest)

	// empty
	em = newEventMocker(withNumber(0))
	rest, err = c.sendEvents(em.events, false)
	assert.Nil(t, err)
	assert.Empty(t, rest)
}
