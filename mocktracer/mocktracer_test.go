package mocktracer

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
)

func TestMockTracer_StartSpan(t *testing.T) {
	tracer := New()
	span1 := tracer.StartSpan(
		"a",
		opentracing.Tags(map[string]interface{}{"x": "y"}))

	span2 := span1.Tracer().StartSpan(
		"", opentracing.ChildOf(span1.Context()))
	span2.Finish()
	span1.Finish()
	spans := tracer.FinishedSpans()
	assert.Equal(t, 2, len(spans))

	parent := spans[1]
	child := spans[0]
	assert.Equal(t, map[string]interface{}{"x": "y"}, parent.Tags())
	assert.Equal(t, child.ParentID, parent.Context().(MockSpanContext).SpanID)
}

func TestMockSpan_SetOperationName(t *testing.T) {
	tracer := New()
	span := tracer.StartSpan("")
	span.SetOperationName("x")
	assert.Equal(t, "x", span.(*MockSpan).OperationName)
}

func TestMockSpanContext_Baggage(t *testing.T) {
	tracer := New()
	span := tracer.StartSpan("x")
	span.SetBaggageItem("x", "y")
	assert.Equal(t, "y", span.BaggageItem("x"))
	assert.Equal(t, map[string]string{"x": "y"}, span.Context().(MockSpanContext).Baggage)

	baggage := make(map[string]string)
	span.Context().ForeachBaggageItem(func(k, v string) bool {
		baggage[k] = v
		return true
	})
	assert.Equal(t, map[string]string{"x": "y"}, baggage)

	span.SetBaggageItem("a", "b")
	baggage = make(map[string]string)
	span.Context().ForeachBaggageItem(func(k, v string) bool {
		baggage[k] = v
		return false // exit early
	})
	assert.Equal(t, 2, len(span.Context().(MockSpanContext).Baggage))
	assert.Equal(t, 1, len(baggage))
}

func TestMockSpan_Tag(t *testing.T) {
	tracer := New()
	span := tracer.StartSpan("x")
	span.SetTag("x", "y")
	assert.Equal(t, "y", span.(*MockSpan).Tag("x"))
}

func TestMockSpan_Tags(t *testing.T) {
	tracer := New()
	span := tracer.StartSpan("x")
	span.SetTag("x", "y")
	assert.Equal(t, map[string]interface{}{"x": "y"}, span.(*MockSpan).Tags())
}

func TestMockTracer_FinishedSpans_and_Reset(t *testing.T) {
	tracer := New()
	span := tracer.StartSpan("x")
	span.SetTag("x", "y")
	span.Finish()
	spans := tracer.FinishedSpans()
	assert.Equal(t, 1, len(spans))
	assert.Equal(t, map[string]interface{}{"x": "y"}, spans[0].Tags())

	tracer.Reset()
	spans = tracer.FinishedSpans()
	assert.Equal(t, 0, len(spans))
}

func TestMockSpan_Logs(t *testing.T) {
	tracer := New()
	span := tracer.StartSpan("x")
	span.LogEvent("x")
	span.LogEventWithPayload("y", "z")
	span.Log(opentracing.LogData{Event: "a"})
	span.FinishWithOptions(opentracing.FinishOptions{
		BulkLogData: []opentracing.LogData{{Event: "f"}}})
	spans := tracer.FinishedSpans()
	assert.Equal(t, 1, len(spans))
	assert.Equal(t, []opentracing.LogData{
		{Event: "x"},
		{Event: "y", Payload: "z"},
		{Event: "a"},
		{Event: "f"},
	}, spans[0].Logs())
}

func TestMockTracer_Propagation(t *testing.T) {
	textCarrier := func() interface{} {
		return opentracing.TextMapCarrier(make(map[string]string))
	}
	textLen := func (c interface{}) int {
		return len(c.(opentracing.TextMapCarrier))
	}

	httpCarrier := func() interface{} {
		httpHeaders := http.Header(make(map[string][]string))
		return opentracing.HTTPHeadersCarrier(httpHeaders)
	}
	httpLen := func (c interface{}) int {
		return len(c.(opentracing.HTTPHeadersCarrier))
	}

	tests := []struct {
		sampled bool
		format  opentracing.BuiltinFormat
		carrier func() interface{}
		len func(interface{}) int
	}{
		{sampled: true, format: opentracing.TextMap, carrier: textCarrier, len: textLen},
		{sampled: false, format: opentracing.TextMap, carrier: textCarrier, len: textLen},
		{sampled: true, format: opentracing.HTTPHeaders, carrier: httpCarrier, len: httpLen},
		{sampled: false, format: opentracing.HTTPHeaders, carrier: httpCarrier, len: httpLen},
	}
	for _, test := range tests {
		tracer := New()
		span := tracer.StartSpan("x")
		span.SetBaggageItem("x", "y:z") // colon should be URL encoded as %3A
		if !test.sampled {
			ext.SamplingPriority.Set(span, 0)
		}
		mSpan := span.(*MockSpan)

		assert.Equal(t, opentracing.ErrUnsupportedFormat,
			tracer.Inject(span.Context(), opentracing.Binary, nil))
		assert.Equal(t, opentracing.ErrInvalidCarrier,
			tracer.Inject(span.Context(), opentracing.TextMap, span))

		carrier := test.carrier()

		err := tracer.Inject(span.Context(), test.format, carrier)
		require.NoError(t, err)
		assert.Equal(t, 4, test.len(carrier), "expect baggage + 2 ids + sampled")
		if test.format == opentracing.HTTPHeaders {
			c := carrier.(opentracing.HTTPHeadersCarrier)
			assert.Equal(t, "y%3Az", c["Mockpfx-Baggage-X"][0])
		}

		_, err = tracer.Extract(opentracing.Binary, nil)
		assert.Equal(t, opentracing.ErrUnsupportedFormat, err)
		_, err = tracer.Extract(opentracing.TextMap, tracer)
		assert.Equal(t, opentracing.ErrInvalidCarrier, err)

		extractedContext, err := tracer.Extract(test.format, carrier)
		require.NoError(t, err)
		assert.Equal(t, mSpan.SpanContext.TraceID, extractedContext.(MockSpanContext).TraceID)
		assert.Equal(t, mSpan.SpanContext.SpanID, extractedContext.(MockSpanContext).SpanID)
		assert.Equal(t, test.sampled, extractedContext.(MockSpanContext).Sampled)
		assert.Equal(t, "y:z", extractedContext.(MockSpanContext).Baggage["x"])
	}
}
