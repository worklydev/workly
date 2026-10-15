package api

import (
	"context"
	"time"
)

// activityRanges are the ranges GET /v1/activity offers, and their buckets.
var activityRanges = map[GetActivityParamsRange]struct{ span, bucket time.Duration }{
	GetActivityParamsRangeN1h:  {time.Hour, 5 * time.Minute},
	GetActivityParamsRangeN24h: {24 * time.Hour, time.Hour},
	GetActivityParamsRangeN7d:  {7 * 24 * time.Hour, 6 * time.Hour},
}

func (h Handlers) GetActivity(ctx context.Context, req GetActivityRequestObject) (GetActivityResponseObject, error) {
	r := GetActivityParamsRangeN24h
	if req.Params.Range != nil {
		r = *req.Params.Range
	}
	spec, ok := activityRanges[r]
	if !ok {
		return nil, invalid("range must be 1h, 24h or 7d")
	}
	// The last bucket is the current one, partly over. Truncate aligns to
	// UTC like the store's buckets, which count from the Unix epoch.
	n := int(spec.span / spec.bucket)
	last := time.Now().UTC().Truncate(spec.bucket)
	first := last.Add(-time.Duration(n-1) * spec.bucket)

	counts, err := h.Store.TaskActivity(ctx, workspaceID(ctx), first, spec.bucket)
	if err != nil {
		return nil, err
	}
	out := GetActivity200JSONResponse{
		Range:         ActivityRange(r),
		BucketSeconds: int(spec.bucket.Seconds()),
		Buckets:       make([]ActivityBucket, n),
	}
	for i := range out.Buckets {
		out.Buckets[i] = ActivityBucket{Start: first.Add(time.Duration(i) * spec.bucket), Counts: map[string]int{}}
	}
	for _, c := range counts {
		if i := int(c.Start.Sub(first) / spec.bucket); i >= 0 && i < n {
			out.Buckets[i].Counts[c.Queue] += c.Count
		}
	}
	if h.Retention > 0 {
		s := seconds(h.Retention)
		out.RetentionSeconds = &s
	}
	return out, nil
}
