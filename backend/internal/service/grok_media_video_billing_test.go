package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type grokVideoPendingOwnerCacheStub struct {
	pending []byte
}

func (s *grokVideoPendingOwnerCacheStub) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, ErrStickySessionNotFound
}

func (s *grokVideoPendingOwnerCacheStub) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func (s *grokVideoPendingOwnerCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *grokVideoPendingOwnerCacheStub) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (s *grokVideoPendingOwnerCacheStub) SetGrokVideoPendingBilling(_ context.Context, _ string, payload []byte, _ time.Duration) error {
	s.pending = append([]byte(nil), payload...)
	return nil
}

func (s *grokVideoPendingOwnerCacheStub) GetGrokVideoPendingBilling(context.Context, string) ([]byte, error) {
	return append([]byte(nil), s.pending...), nil
}

func (s *grokVideoPendingOwnerCacheStub) ClaimGrokVideoBilled(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

func (s *grokVideoPendingOwnerCacheStub) ReleaseGrokVideoBilled(context.Context, string) error {
	return nil
}

func TestGrokVideoE2EDurationFromCreatedAt(t *testing.T) {
	t.Parallel()
	created := time.Now().UTC().Add(-45 * time.Second)
	d := GrokVideoE2EDuration(created.Format(time.RFC3339Nano), time.Now().UTC())
	require.GreaterOrEqual(t, d, 44*time.Second)
	require.LessOrEqual(t, d, 47*time.Second)

	require.Equal(t, time.Duration(0), GrokVideoE2EDuration("", time.Now()))
	require.Equal(t, time.Duration(0), GrokVideoE2EDuration("not-a-time", time.Now()))
	// Future CreatedAt clamps to zero (clock skew).
	require.Equal(t, time.Duration(0), GrokVideoE2EDuration(time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), time.Now()))
}

func TestGrokVideoPendingCreatedAtStampOnStoreShape(t *testing.T) {
	t.Parallel()
	// GrokVideoPendingCreatedAtNow must be parseable by GrokVideoE2EDuration.
	stamp := GrokVideoPendingCreatedAtNow()
	require.NotEmpty(t, stamp)
	d := GrokVideoE2EDuration(stamp, time.Now().UTC().Add(2*time.Second))
	require.GreaterOrEqual(t, d, time.Second)
	require.LessOrEqual(t, d, 3*time.Second)
}

func TestResolveGrokMediaVideoRequestAccountFallsBackToPendingOwner(t *testing.T) {
	t.Parallel()
	cache := &grokVideoPendingOwnerCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	groupID := int64(17)
	err := svc.StoreGrokVideoPendingBilling(context.Background(), "video-task", 23, 29, GrokVideoPendingBilling{
		AccountID:            260,
		Model:                "grok-imagine-video",
		VideoResolution:      "720p",
		VideoDurationSeconds: 6,
	})
	require.NoError(t, err)

	accountID, err := svc.ResolveGrokMediaVideoRequestAccount(context.Background(), &groupID, "video-task", 23, 29)
	require.NoError(t, err)
	require.Equal(t, int64(260), accountID)
}

func TestSelectGrokMediaVideoRequestAccountUsesExactPendingOwner(t *testing.T) {
	t.Parallel()
	groupID := int64(17)
	cache := &grokVideoPendingOwnerCacheStub{}
	accounts := []Account{
		{ID: 259, Platform: PlatformGrok, Status: StatusActive, Schedulable: true, GroupIDs: []int64{groupID}},
		{ID: 260, Platform: PlatformGrok, Status: StatusActive, Schedulable: true, GroupIDs: []int64{groupID}},
	}
	svc := &OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: accounts},
		cache:       cache,
	}
	require.NoError(t, svc.StoreGrokVideoPendingBilling(context.Background(), "video-task", 23, 29, GrokVideoPendingBilling{
		AccountID: 260,
		Model:     "grok-imagine-video",
	}))
	ownerID, err := svc.ResolveGrokMediaVideoRequestAccount(context.Background(), &groupID, "video-task", 23, 29)
	require.NoError(t, err)
	require.Equal(t, int64(260), ownerID)

	selection, err := svc.SelectGrokMediaVideoRequestAccount(context.Background(), &groupID, ownerID)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(260), selection.Account.ID)
	require.True(t, selection.Acquired)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestIsGrokVideoPendingInStartupWindow(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	require.True(t, IsGrokVideoPendingInStartupWindow(&GrokVideoPendingBilling{
		CreatedAt: now.Add(-2 * time.Second).Format(time.RFC3339Nano),
	}, now))
	require.False(t, IsGrokVideoPendingInStartupWindow(&GrokVideoPendingBilling{
		CreatedAt: now.Add(-13 * time.Second).Format(time.RFC3339Nano),
	}, now))
	require.False(t, IsGrokVideoPendingInStartupWindow(&GrokVideoPendingBilling{CreatedAt: "invalid"}, now))
}

func TestIsGrokVideoStatusBillable(t *testing.T) {
	t.Parallel()
	// Official success: status=done + video.url
	require.True(t, IsGrokVideoStatusBillable([]byte(`{
		"status":"done",
		"model":"grok-imagine-video-1.5",
		"video":{"url":"https://vidgen.x.ai/x.mp4","duration":8,"respect_moderation":true}
	}`)))

	// Official non-success states
	require.False(t, IsGrokVideoStatusBillable(nil))
	require.False(t, IsGrokVideoStatusBillable([]byte(`{"status":"pending"}`)))
	require.False(t, IsGrokVideoStatusBillable([]byte(`{"status":"expired"}`)))
	require.False(t, IsGrokVideoStatusBillable([]byte(`{"status":"failed"}`)))
	// done without video.url is not billable
	require.False(t, IsGrokVideoStatusBillable([]byte(`{"status":"done"}`)))
	// URL alone (legacy/non-official shapes) is not enough
	require.False(t, IsGrokVideoStatusBillable([]byte(`{"url":"https://example.com/v.mp4"}`)))
	require.False(t, IsGrokVideoStatusBillable([]byte(`{"download_url":"/v1/videos/task/content"}`)))
	// "completed" is not the official enum value
	require.False(t, IsGrokVideoStatusBillable([]byte(`{"status":"completed","video":{"url":"https://vidgen.x.ai/x.mp4"}}`)))
}

func TestExtractGrokVideoBillingFromStatusBodyPrefersUpstreamParams(t *testing.T) {
	t.Parallel()
	pending := &GrokVideoPendingBilling{
		Model:                "pending-model",
		BillingModel:         "pending-billing",
		UpstreamModel:        "pending-upstream",
		VideoResolution:      VideoBillingResolution720P,
		VideoDurationSeconds: 8,
	}
	// Official completed body from docs.x.ai Video Generation.
	body := []byte(`{
		"status":"done",
		"model":"grok-imagine-video-1.5",
		"video":{"url":"https://vidgen.x.ai/signed.mp4","duration":12,"respect_moderation":true}
	}`)
	result := ExtractGrokVideoBillingFromStatusBody(body, pending, "req-1")
	require.NotNil(t, result)
	require.Equal(t, 1, result.VideoCount)
	require.Equal(t, "grok-imagine-video-1.5", result.Model)
	// Resolution is not in official status response — use create-time request.
	require.Equal(t, VideoBillingResolution720P, result.VideoResolution)
	// Duration prefers official video.duration.
	require.Equal(t, 12, result.VideoDurationSeconds)
}

func TestExtractGrokVideoBillingFromStatusBodyFallsBackToPending(t *testing.T) {
	t.Parallel()
	pending := &GrokVideoPendingBilling{
		Model:                "create-model",
		BillingModel:         "create-billing",
		UpstreamModel:        "create-upstream",
		VideoResolution:      VideoBillingResolution1080P,
		VideoDurationSeconds: 10,
	}
	// done + video.url, but no model/duration in body.
	body := []byte(`{"status":"done","video":{"url":"https://vidgen.x.ai/signed.mp4"}}`)
	result := ExtractGrokVideoBillingFromStatusBody(body, pending, "req-2")
	require.NotNil(t, result)
	require.Equal(t, "create-billing", result.BillingModel)
	require.Equal(t, "create-upstream", result.UpstreamModel)
	require.Equal(t, VideoBillingResolution1080P, result.VideoResolution)
	require.Equal(t, 10, result.VideoDurationSeconds)
}

func TestExtractGrokVideoBillingRejectsNonDoneStatus(t *testing.T) {
	t.Parallel()
	pending := &GrokVideoPendingBilling{Model: "m", VideoDurationSeconds: 8, VideoResolution: "720p"}
	require.Nil(t, ExtractGrokVideoBillingFromStatusBody(
		[]byte(`{"status":"pending","video":{"url":"https://vidgen.x.ai/x.mp4","duration":8}}`),
		pending, "req",
	))
	require.Nil(t, ExtractGrokVideoBillingFromStatusBody(
		[]byte(`{"status":"completed","video":{"url":"https://vidgen.x.ai/x.mp4","duration":8}}`),
		pending, "req",
	))
}

func TestGrokMediaUsageFromResponseVideoCreateDoesNotBill(t *testing.T) {
	t.Parallel()
	info := GrokMediaRequestInfo{Model: "grok-imagine-video", Resolution: "720p", DurationSeconds: 10}
	meta := grokMediaUsageFromResponse(GrokMediaEndpointVideosGenerations, info, []byte(`{"request_id":"v1"}`))
	require.Equal(t, "v1", meta.ResponseID)
	require.Equal(t, 0, meta.VideoCount)
	require.Equal(t, 10, meta.VideoDurationSeconds)
	require.Equal(t, VideoBillingResolution720P, meta.VideoResolution)
}

func TestForwardGrokMediaWithBeforeResponsePersistsBeforeCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"grok-imagine-video","prompt":"waves","duration":6}`)
	account := &Account{
		ID:          63,
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://api.x.ai/v1",
		},
	}

	t.Run("callback runs before downstream write", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"request_id":"video-task-123"}`)),
		}}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		callbackCalled := false

		result, err := svc.ForwardGrokMediaWithBeforeResponse(
			context.Background(), c, account, GrokMediaEndpointVideosGenerations, "", body, "application/json",
			func(result *OpenAIForwardResult) error {
				callbackCalled = true
				require.Empty(t, recorder.Body.String())
				require.Equal(t, "video-task-123", result.ResponseID)
				return nil
			},
		)

		require.NoError(t, err)
		require.True(t, callbackCalled)
		require.Equal(t, "video-task-123", result.ResponseID)
		require.JSONEq(t, `{"request_id":"video-task-123"}`, recorder.Body.String())
	})

	t.Run("callback failure leaves downstream uncommitted", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"request_id":"video-task-456"}`)),
		}}
		svc := &OpenAIGatewayService{httpUpstream: upstream}

		result, err := svc.ForwardGrokMediaWithBeforeResponse(
			context.Background(), c, account, GrokMediaEndpointVideosGenerations, "", body, "application/json",
			func(*OpenAIForwardResult) error { return errors.New("redis unavailable") },
		)

		require.ErrorContains(t, err, "prepare grok media response state")
		require.Nil(t, result)
		require.Empty(t, recorder.Body.String())
	})
}

func TestForwardGrokMediaRecentStatusNotFoundReturnsPendingAfterBoundedRetries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/video-task-404", nil)
	responses := make([]*http.Response, 0, grokVideoStartupNotFoundMaxAttempts)
	for i := 0; i < grokVideoStartupNotFoundMaxAttempts; i++ {
		responses = append(responses, &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"message":"not registered yet"}}`)),
		})
	}
	upstream := &httpUpstreamRecorder{responses: responses}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          260,
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://api.x.ai/v1",
		},
	}

	result, err := svc.ForwardGrokMedia(
		WithGrokVideoStartupNotFoundFallback(context.Background()),
		c,
		account,
		GrokMediaEndpointVideoStatus,
		"video-task-404",
		nil,
		"",
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "video-task-404", result.ResponseID)
	require.Len(t, upstream.requests, grokVideoStartupNotFoundMaxAttempts)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.JSONEq(t, `{"request_id":"video-task-404","status":"pending"}`, recorder.Body.String())
}

func TestGrokMediaUsageFromResponseVideoStatusBillsOnOfficialDone(t *testing.T) {
	t.Parallel()
	meta := grokMediaUsageFromResponse(
		GrokMediaEndpointVideoStatus,
		GrokMediaRequestInfo{},
		[]byte(`{"status":"done","model":"grok-imagine-video-1.5","video":{"url":"https://vidgen.x.ai/a.mp4","duration":9}}`),
	)
	require.Equal(t, 1, meta.VideoCount)
	require.Equal(t, 9, meta.VideoDurationSeconds)
	require.Equal(t, "grok-imagine-video-1.5", meta.Model)

	// Official non-done must not set billable units.
	pendingOnly := grokMediaUsageFromResponse(
		GrokMediaEndpointVideoStatus,
		GrokMediaRequestInfo{},
		[]byte(`{"status":"pending"}`),
	)
	require.Equal(t, 0, pendingOnly.VideoCount)

	// completed is not official done.
	completed := grokMediaUsageFromResponse(
		GrokMediaEndpointVideoStatus,
		GrokMediaRequestInfo{},
		[]byte(`{"status":"completed","video":{"url":"https://vidgen.x.ai/a.mp4","duration":9}}`),
	)
	require.Equal(t, 0, completed.VideoCount)
}

func TestObserveGrokVideoCompletionPendingThenDone(t *testing.T) {
	t.Parallel()
	options := grokVideoCompletionObserverOptions{
		MaxDuration:         time.Second,
		MaxAttempts:         5,
		MaxConsecutive404:   3,
		NotFoundRetryWindow: time.Second,
	}
	var fetchCalls int
	var billed *OpenAIForwardResult
	err := observeGrokVideoCompletion(context.Background(), options, func(context.Context) (*grokVideoStatusObservation, error) {
		fetchCalls++
		if fetchCalls == 1 {
			return &grokVideoStatusObservation{HTTPStatus: http.StatusOK, Status: "pending"}, nil
		}
		return &grokVideoStatusObservation{
			HTTPStatus: http.StatusOK,
			Status:     "done",
			Result:     &OpenAIForwardResult{ResponseID: "video-task", VideoCount: 1},
		}, nil
	}, func(result *OpenAIForwardResult) {
		billed = result
	})

	require.NoError(t, err)
	require.Equal(t, 2, fetchCalls)
	require.NotNil(t, billed)
	require.Equal(t, "video-task", billed.ResponseID)
}

func TestObserveGrokVideoCompletionStopsOnTerminalStates(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"done", "failed", "expired"} {
		status := status
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			fetchCalls := 0
			billCalls := 0
			err := observeGrokVideoCompletion(context.Background(), grokVideoCompletionObserverOptions{
				MaxDuration:         time.Second,
				MaxAttempts:         5,
				MaxConsecutive404:   3,
				NotFoundRetryWindow: time.Second,
			}, func(context.Context) (*grokVideoStatusObservation, error) {
				fetchCalls++
				// done without video.url produces no billable Result and must still stop.
				return &grokVideoStatusObservation{HTTPStatus: http.StatusOK, Status: status}, nil
			}, func(*OpenAIForwardResult) {
				billCalls++
			})

			require.NoError(t, err)
			require.Equal(t, 1, fetchCalls)
			require.Zero(t, billCalls)
		})
	}
}

func TestObserveGrokVideoCompletionStopsAfterConsecutiveNotFound(t *testing.T) {
	t.Parallel()
	options := grokVideoCompletionObserverOptions{
		MaxDuration:         time.Second,
		MaxAttempts:         10,
		MaxConsecutive404:   3,
		NotFoundRetryWindow: time.Second,
	}
	var fetchCalls int
	err := observeGrokVideoCompletion(context.Background(), options, func(context.Context) (*grokVideoStatusObservation, error) {
		fetchCalls++
		return &grokVideoStatusObservation{HTTPStatus: http.StatusNotFound}, nil
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 3, fetchCalls)
}

func TestDefaultObserveGrokVideoNotFoundRetriesCoverStartupWindow(t *testing.T) {
	t.Parallel()
	require.GreaterOrEqual(t,
		time.Duration(defaultGrokVideoObserverMaxConsecutive404-1)*defaultGrokVideoObserverPollInterval,
		defaultGrokVideoObserverNotFoundRetryWindow,
	)
}

func TestFetchGrokVideoStatusObservationRespectsAccountConcurrency(t *testing.T) {
	t.Parallel()
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{
			acquireResults: map[int64]bool{260: false},
		}),
	}
	account := &Account{
		ID:          260,
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://api.x.ai/v1",
		},
	}

	observation, err := svc.fetchGrokVideoStatusObservation(context.Background(), account, "video-task")

	require.Nil(t, observation)
	require.ErrorContains(t, err, "account concurrency is full")
	require.Empty(t, upstream.requests)
}

func TestObserveGrokVideoCompletionDoesNotRetryNotFoundOutsideStartupWindow(t *testing.T) {
	t.Parallel()
	options := grokVideoCompletionObserverOptions{
		MaxDuration:         time.Second,
		MaxAttempts:         10,
		MaxConsecutive404:   3,
		NotFoundRetryWindow: time.Nanosecond,
	}
	var fetchCalls int
	err := observeGrokVideoCompletion(context.Background(), options, func(context.Context) (*grokVideoStatusObservation, error) {
		fetchCalls++
		time.Sleep(time.Millisecond)
		return &grokVideoStatusObservation{HTTPStatus: http.StatusNotFound}, nil
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 1, fetchCalls)
}

func TestObserveGrokVideoCompletionCapsPendingPolls(t *testing.T) {
	t.Parallel()
	options := grokVideoCompletionObserverOptions{
		MaxDuration:         time.Second,
		MaxAttempts:         4,
		MaxConsecutive404:   3,
		NotFoundRetryWindow: time.Second,
	}
	var fetchCalls int
	err := observeGrokVideoCompletion(context.Background(), options, func(context.Context) (*grokVideoStatusObservation, error) {
		fetchCalls++
		return &grokVideoStatusObservation{HTTPStatus: http.StatusOK, Status: "pending"}, nil
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 4, fetchCalls)
}
