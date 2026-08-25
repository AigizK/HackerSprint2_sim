package journal

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

const (
	formatVersion = byte(1)
	headerSize    = 28
	trailerSize   = 4
)

var (
	frameMagic = [4]byte{'H', 'S', 'J', '1'}
	crcTable   = crc32.MakeTable(crc32.Castagnoli)
)

type recordKind byte

const (
	kindDomainEvents     recordKind = 1
	kindRequestReceived  recordKind = 2
	kindRequestCompleted recordKind = 3
	kindDomainBegin      recordKind = 4
	kindDomainPart       recordKind = 5
	kindDomainCommit     recordKind = 6
)

type frame struct {
	kind       recordKind
	sequence   uint64
	recordedAt time.Time
	payload    []byte
}

func encodeFrame(value frame) []byte {
	result := make([]byte, headerSize+len(value.payload)+trailerSize)
	copy(result[:4], frameMagic[:])
	result[4] = formatVersion
	result[5] = byte(value.kind)
	binary.BigEndian.PutUint64(result[8:16], value.sequence)
	binary.BigEndian.PutUint64(result[16:24], uint64(value.recordedAt.UnixNano()))
	binary.BigEndian.PutUint32(result[24:28], uint32(len(value.payload)))
	copy(result[headerSize:], value.payload)
	binary.BigEndian.PutUint32(result[len(result)-trailerSize:], crc32.Checksum(result[:len(result)-trailerSize], crcTable))
	return result
}

func readFrame(reader io.Reader) (frame, int64, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return frame{}, 0, err
	}
	if !bytes.Equal(header[:4], frameMagic[:]) || header[4] != formatVersion {
		return frame{}, 0, fmt.Errorf("invalid journal frame header")
	}
	payloadLength := binary.BigEndian.Uint32(header[24:28])
	payloadAndCRC := make([]byte, int(payloadLength)+trailerSize)
	if _, err := io.ReadFull(reader, payloadAndCRC); err != nil {
		return frame{}, 0, err
	}
	checksumInput := append(append([]byte(nil), header...), payloadAndCRC[:payloadLength]...)
	want := binary.BigEndian.Uint32(payloadAndCRC[payloadLength:])
	if got := crc32.Checksum(checksumInput, crcTable); got != want {
		return frame{}, 0, fmt.Errorf("journal frame checksum mismatch")
	}
	return frame{
		kind: recordKind(header[5]), sequence: binary.BigEndian.Uint64(header[8:16]),
		recordedAt: time.Unix(0, int64(binary.BigEndian.Uint64(header[16:24]))).UTC(),
		payload:    append([]byte(nil), payloadAndCRC[:payloadLength]...),
	}, int64(headerSize) + int64(payloadLength) + trailerSize, nil
}

func encodeDomainBatch(firstVersion uint64, domainEvents []events.Event) ([]byte, error) {
	var buffer bytes.Buffer
	_ = binary.Write(&buffer, binary.BigEndian, firstVersion)
	_ = binary.Write(&buffer, binary.BigEndian, uint32(len(domainEvents)))
	for _, event := range domainEvents {
		eventType, payload, err := simulation.EncodeEvent(event)
		if err != nil {
			return nil, err
		}
		if len(eventType) > int(^uint16(0)) {
			return nil, fmt.Errorf("event type is too long")
		}
		_ = binary.Write(&buffer, binary.BigEndian, uint16(len(eventType)))
		buffer.WriteString(eventType)
		_ = binary.Write(&buffer, binary.BigEndian, uint32(len(payload)))
		buffer.Write(payload)
	}
	return buffer.Bytes(), nil
}

func decodeDomainBatch(payload []byte) ([]simulation.StoredEvent, error) {
	reader := bytes.NewReader(payload)
	var first uint64
	var count uint32
	if err := binary.Read(reader, binary.BigEndian, &first); err != nil {
		return nil, err
	}
	if err := binary.Read(reader, binary.BigEndian, &count); err != nil {
		return nil, err
	}
	result := make([]simulation.StoredEvent, 0, count)
	for index := uint32(0); index < count; index++ {
		var typeLength uint16
		if err := binary.Read(reader, binary.BigEndian, &typeLength); err != nil {
			return nil, err
		}
		eventType := make([]byte, typeLength)
		if _, err := io.ReadFull(reader, eventType); err != nil {
			return nil, err
		}
		var payloadLength uint32
		if err := binary.Read(reader, binary.BigEndian, &payloadLength); err != nil {
			return nil, err
		}
		eventPayload := make([]byte, payloadLength)
		if _, err := io.ReadFull(reader, eventPayload); err != nil {
			return nil, err
		}
		event, err := simulation.DecodeEvent(string(eventType), eventPayload)
		if err != nil {
			return nil, err
		}
		result = append(result, simulation.StoredEvent{Version: first + uint64(index), Event: event})
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("domain batch contains trailing bytes")
	}
	return result, nil
}
