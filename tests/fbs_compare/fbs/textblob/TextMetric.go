package textblob

import (
	flatbuffers "github.com/google/flatbuffers/go"
)

type TextMetric struct {
	_tab flatbuffers.Table
}

func GetRootAsTextMetric(buf []byte, offset flatbuffers.UOffsetT) *TextMetric {
	n := flatbuffers.GetUOffsetT(buf[offset:])
	x := &TextMetric{}
	x.Init(buf, n+offset)
	return x
}

func FinishTextMetricBuffer(builder *flatbuffers.Builder, offset flatbuffers.UOffsetT) {
	builder.Finish(offset)
}

func GetSizePrefixedRootAsTextMetric(buf []byte, offset flatbuffers.UOffsetT) *TextMetric {
	n := flatbuffers.GetUOffsetT(buf[offset+flatbuffers.SizeUint32:])
	x := &TextMetric{}
	x.Init(buf, n+offset+flatbuffers.SizeUint32)
	return x
}

func FinishSizePrefixedTextMetricBuffer(builder *flatbuffers.Builder, offset flatbuffers.UOffsetT) {
	builder.FinishSizePrefixed(offset)
}

func (rcv *TextMetric) Init(buf []byte, i flatbuffers.UOffsetT) {
	rcv._tab.Bytes = buf
	rcv._tab.Pos = i
}

func (rcv *TextMetric) Table() flatbuffers.Table {
	return rcv._tab
}

func (rcv *TextMetric) Id() uint64 {
	o := flatbuffers.UOffsetT(rcv._tab.Offset(4))
	if o != 0 {
		return rcv._tab.GetUint64(o + rcv._tab.Pos)
	}
	return 0
}

func (rcv *TextMetric) MutateId(n uint64) bool {
	return rcv._tab.MutateUint64Slot(4, n)
}

func (rcv *TextMetric) Timestamp(j int) int64 {
	o := flatbuffers.UOffsetT(rcv._tab.Offset(6))
	if o != 0 {
		a := rcv._tab.Vector(o)
		return rcv._tab.GetInt64(a + flatbuffers.UOffsetT(j*8))
	}
	return 0
}

func (rcv *TextMetric) TimestampLength() int {
	o := flatbuffers.UOffsetT(rcv._tab.Offset(6))
	if o != 0 {
		return rcv._tab.VectorLen(o)
	}
	return 0
}

func (rcv *TextMetric) MutateTimestamp(j int, n int64) bool {
	o := flatbuffers.UOffsetT(rcv._tab.Offset(6))
	if o != 0 {
		a := rcv._tab.Vector(o)
		return rcv._tab.MutateInt64(a+flatbuffers.UOffsetT(j*8), n)
	}
	return false
}

func (rcv *TextMetric) Value(j int) []byte {
	o := flatbuffers.UOffsetT(rcv._tab.Offset(8))
	if o != 0 {
		a := rcv._tab.Vector(o)
		return rcv._tab.ByteVector(a + flatbuffers.UOffsetT(j*4))
	}
	return nil
}

func (rcv *TextMetric) ValueLength() int {
	o := flatbuffers.UOffsetT(rcv._tab.Offset(8))
	if o != 0 {
		return rcv._tab.VectorLen(o)
	}
	return 0
}

func (rcv *TextMetric) Tag(j int) []byte {
	o := flatbuffers.UOffsetT(rcv._tab.Offset(10))
	if o != 0 {
		a := rcv._tab.Vector(o)
		return rcv._tab.ByteVector(a + flatbuffers.UOffsetT(j*4))
	}
	return nil
}

func (rcv *TextMetric) TagLength() int {
	o := flatbuffers.UOffsetT(rcv._tab.Offset(10))
	if o != 0 {
		return rcv._tab.VectorLen(o)
	}
	return 0
}

func TextMetricStart(builder *flatbuffers.Builder) {
	builder.StartObject(4)
}

func TextMetricAddId(builder *flatbuffers.Builder, id uint64) {
	builder.PrependUint64Slot(0, id, 0)
}

func TextMetricAddTimestamp(builder *flatbuffers.Builder, timestamp flatbuffers.UOffsetT) {
	builder.PrependUOffsetTSlot(1, flatbuffers.UOffsetT(timestamp), 0)
}

func TextMetricStartTimestampVector(builder *flatbuffers.Builder, numElems int) flatbuffers.UOffsetT {
	return builder.StartVector(8, numElems, 8)
}

func TextMetricAddValue(builder *flatbuffers.Builder, value flatbuffers.UOffsetT) {
	builder.PrependUOffsetTSlot(2, flatbuffers.UOffsetT(value), 0)
}

func TextMetricStartValueVector(builder *flatbuffers.Builder, numElems int) flatbuffers.UOffsetT {
	return builder.StartVector(4, numElems, 4)
}

func TextMetricAddTag(builder *flatbuffers.Builder, tag flatbuffers.UOffsetT) {
	builder.PrependUOffsetTSlot(3, flatbuffers.UOffsetT(tag), 0)
}

func TextMetricStartTagVector(builder *flatbuffers.Builder, numElems int) flatbuffers.UOffsetT {
	return builder.StartVector(4, numElems, 4)
}

func TextMetricEnd(builder *flatbuffers.Builder) flatbuffers.UOffsetT {
	return builder.EndObject()
}
