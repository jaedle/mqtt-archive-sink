package app_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArchivesPublishedMessages(t *testing.T) {
	dir := t.TempDir()
	broker := startBroker(t, freeAddr(t))
	sink := startSink(t, dir, broker.addr)
	broker.waitSubscribed()

	binary := []byte{0xff, 0xfe, 0x01}
	broker.publish("sensors/temp", []byte("21.5"))
	broker.publish("sensors/bin", binary)
	broker.publish("sensors/hum", []byte(`{"value":60}`))
	waitForLineCount(t, todayFile(dir), 3)
	sink.stop()

	records := readRecords(t, todayFile(dir))
	require.Len(t, records, 3)
	assert.Equal(t, "sensors/temp", records[0].Topic)
	assert.Equal(t, "21.5", *records[0].Payload)
	assert.Equal(t, "sensors/bin", records[1].Topic)
	assert.Nil(t, records[1].Payload, "non-UTF-8 payload must use payload_b64")
	assert.Equal(t, binary, decodeBase64(t, *records[1].PayloadB64))
	assert.Equal(t, `{"value":60}`, *records[2].Payload)
	assertTimestampsAreRecent(t, records)
}

func TestHeartbeatTouchedWithoutTraffic(t *testing.T) {
	dir := t.TempDir()
	broker := startBroker(t, freeAddr(t))
	sink := startSink(t, dir, broker.addr)
	broker.waitSubscribed()

	waitForFreshHeartbeat(t, sink.dir+"/heartbeat")

	sink.stop()
}

func TestReconnectsAfterBrokerRestart(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)
	broker := startBroker(t, addr)
	sink := startSink(t, dir, addr)
	broker.waitSubscribed()

	broker.publish("t/1", []byte("before"))
	waitForLineCount(t, todayFile(dir), 1)
	broker.stop()
	broker = startBroker(t, addr)
	broker.waitSubscribed()
	broker.publish("t/2", []byte("after"))
	waitForAtLeastLines(t, todayFile(dir), 2)
	sink.stop()

	topics := topicsOf(readRecords(t, todayFile(dir)))
	assert.Contains(t, topics, "t/1")
	assert.Contains(t, topics, "t/2")
}
