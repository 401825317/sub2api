package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GrokImages handles xAI image generation/editing through Grok groups.
func (h *OpenAIGatewayHandler) GrokImages(c *gin.Context) {
	endpoint := service.GrokMediaEndpointImagesGenerations
	if strings.Contains(c.Request.URL.Path, "/images/edits") {
		endpoint = service.GrokMediaEndpointImagesEdits
	}
	h.handleGrokMedia(c, endpoint, "")
}

// GrokVideoGeneration handles xAI video generation through Grok groups.
func (h *OpenAIGatewayHandler) GrokVideoGeneration(c *gin.Context) {
	h.handleGrokMedia(c, service.GrokMediaEndpointVideosGenerations, "")
}

// GrokVideoEdit handles asynchronous xAI video edits through Grok groups.
func (h *OpenAIGatewayHandler) GrokVideoEdit(c *gin.Context) {
	h.handleGrokMedia(c, service.GrokMediaEndpointVideosEdits, "")
}

// GrokVideoExtension handles asynchronous xAI video extensions through Grok groups.
func (h *OpenAIGatewayHandler) GrokVideoExtension(c *gin.Context) {
	h.handleGrokMedia(c, service.GrokMediaEndpointVideosExtensions, "")
}

// GrokVideoStatus handles xAI video status retrieval through Grok groups.
func (h *OpenAIGatewayHandler) GrokVideoStatus(c *gin.Context) {
	h.handleGrokMedia(c, service.GrokMediaEndpointVideoStatus, c.Param("request_id"))
}

// GrokVideoContent proxies downloadable video content through the task's upstream account.
func (h *OpenAIGatewayHandler) GrokVideoContent(c *gin.Context) {
	h.handleGrokMedia(c, service.GrokMediaEndpointVideoContent, c.Param("request_id"))
}

func (h *OpenAIGatewayHandler) handleGrokMedia(c *gin.Context, endpoint service.GrokMediaEndpoint, requestID string) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}

	reqLog := requestLogger(
		c,
		"handler.openai_gateway.grok_media",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("endpoint", string(endpoint)),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	var body []byte
	var err error
	if endpoint.RequiresRequestBody() {
		body, err = pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			if maxErr, ok := extractMaxBytesError(err); ok {
				h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
				return
			}
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
			return
		}
		if len(body) == 0 {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
			return
		}
	}

	contentType := c.GetHeader("Content-Type")
	requestInfo := service.ParseGrokMediaRequest(contentType, body)
	requestModel := requestInfo.Model
	routingModel := service.NormalizeGrokMediaModelForEndpoint(endpoint, requestModel, requestInfo.HasInputImage())
	if endpoint.IsGenerationRequest() && strings.TrimSpace(requestModel) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if endpoint.IsVideoLookupRequest() && strings.TrimSpace(requestID) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "request_id is required")
		return
	}

	reqLog = reqLog.With(zap.String("model", requestModel))
	setOpsRequestContext(c, requestModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))

	if endpoint.IsGenerationRequest() {
		if !service.GroupAllowsImageGeneration(apiKey.Group) {
			h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
			return
		}
		if moderationBody := requestInfo.ModerationBody(); len(moderationBody) > 0 {
			decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, requestModel, moderationBody)
			if decision != nil && !decision.AllowNextStage {
				h.openAISecurityAuditError(c, decision)
				return
			}
		}
		imageReleaseFunc, acquired := h.acquireImageGenerationSlot(c, streamStarted)
		if !acquired {
			return
		}
		if imageReleaseFunc != nil {
			defer imageReleaseFunc()
		}
	}

	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("grok_media.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	sessionSeed := body
	if len(sessionSeed) == 0 && strings.TrimSpace(requestID) != "" {
		sessionSeed = []byte(requestID)
	}
	sessionHash := h.gatewayService.GenerateExplicitSessionHash(c, sessionSeed)
	boundLookupAccountID := int64(0)
	videoStartupNotFoundFallback := false
	if endpoint.IsVideoLookupRequest() {
		sessionHash = service.GrokMediaVideoRequestSessionHash(requestID, subject.UserID, apiKey.ID)
		boundLookupAccountID, err = h.gatewayService.ResolveGrokMediaVideoRequestAccount(
			c.Request.Context(), apiKey.GroupID, requestID, subject.UserID, apiKey.ID,
		)
		if err != nil || boundLookupAccountID <= 0 {
			reqLog.Info("grok_media.video_lookup_owner_binding_missing", zap.Error(err))
			h.errorResponse(c, http.StatusNotFound, "not_found_error", "Video request not found")
			return
		}
		if endpoint == service.GrokMediaEndpointVideoStatus {
			pending, pendingErr := h.gatewayService.LoadGrokVideoPendingBilling(
				c.Request.Context(), requestID, subject.UserID, apiKey.ID,
			)
			if pendingErr != nil {
				reqLog.Warn("grok_media.video_lookup_pending_load_failed", zap.Error(pendingErr))
			} else {
				videoStartupNotFoundFallback = service.IsGrokVideoPendingInStartupWindow(pending, time.Now())
			}
		}
	}
	// Grok 媒体（图片/视频生成与视频查询）按媒体倍率计费，不在 token 利润门
	// 范围内：显式豁免，防止 service 层防御性装门按文本 D 误过滤媒体请求，
	// 也防止已计费的在途视频任务因绑定账号被门排除而查询返回伪 404。
	requestCtx := service.WithOpenAIProfitControlSuppressed(c.Request.Context())
	if videoStartupNotFoundFallback {
		requestCtx = service.WithGrokVideoStartupNotFoundFallback(requestCtx)
	}
	profitVetoCount := 0
	failedAccountIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	var oauth429FailoverState service.OpenAIOAuth429FailoverState
	mediaEligibilityRejected := false
	switchCount := 0
	videoCreateStartedAt := ""
	if isGrokVideoCreateEndpoint(endpoint) {
		videoCreateStartedAt = service.GrokVideoPendingCreatedAtNow()
	}
	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	routingStart := time.Now()
	requiredCapability := grokMediaRequiredCapability(endpoint)

	for {
		if failoverClientGone(c) {
			return
		}
		var selection *service.AccountSelectionResult
		var scheduleDecision service.OpenAIAccountScheduleDecision
		if boundLookupAccountID > 0 {
			selection, err = h.gatewayService.SelectGrokMediaVideoRequestAccount(
				requestCtx, apiKey.GroupID, boundLookupAccountID,
			)
			scheduleDecision.Layer = "grok_video_task_owner"
		} else {
			selection, scheduleDecision, err = h.gatewayService.SelectAccountWithSchedulerForCapability(
				requestCtx,
				apiKey.GroupID,
				"",
				sessionHash,
				routingModel,
				failedAccountIDs,
				service.OpenAIUpstreamTransportHTTPSSE,
				requiredCapability,
				false,
				false,
				false,
				service.PlatformGrok,
			)
		}
		if err != nil {
			if failoverClientGone(c) {
				reqLog.Info("grok_media.account_select_aborted_client_disconnected", zap.Error(err))
				return
			}
			reqLog.Warn("grok_media.account_select_failed",
				zap.Error(err),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if endpoint.IsGenerationRequest() && errors.Is(err, service.ErrNoAvailableAccounts) &&
				(len(failedAccountIDs) == 0 || (mediaEligibilityRejected && lastFailoverErr == nil)) {
				markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				h.errorResponse(c, http.StatusServiceUnavailable, "grok_media_no_eligible_account", "No eligible Grok media accounts")
				return
			}
			if len(failedAccountIDs) == 0 {
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, requestModel, routingModel, service.PlatformGrok)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				}
				h.errorResponse(c, cls.Status, cls.ErrType, cls.Message)
				return
			}
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, false)
			} else {
				h.errorResponse(c, http.StatusBadGateway, "api_error", "Upstream request failed")
			}
			return
		}
		if selection == nil || selection.Account == nil {
			if endpoint.IsGenerationRequest() {
				markOpsRoutingCapacityLimited(c)
				h.errorResponse(c, http.StatusServiceUnavailable, "grok_media_no_eligible_account", "No eligible Grok media accounts")
				return
			}
			cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, requestModel, routingModel, service.PlatformGrok)
			if !cls.ModelNotFound {
				markOpsRoutingCapacityLimited(c)
			}
			h.errorResponse(c, cls.Status, cls.ErrType, cls.Message)
			return
		}
		if boundLookupAccountID > 0 && selection.Account.ID != boundLookupAccountID {
			reqLog.Warn("grok_media.video_lookup_bound_account_unavailable",
				zap.Int64("bound_account_id", boundLookupAccountID),
				zap.Int64("selected_account_id", selection.Account.ID),
			)
			h.errorResponse(c, http.StatusNotFound, "not_found_error", "Video request not found")
			return
		}

		reqLog.Debug("grok_media.account_schedule_decision",
			zap.String("layer", scheduleDecision.Layer),
			zap.Bool("sticky_session_hit", scheduleDecision.StickySessionHit),
			zap.Int("candidate_count", scheduleDecision.CandidateCount),
			zap.Int("top_k", scheduleDecision.TopK),
			zap.Int64("latency_ms", scheduleDecision.LatencyMs),
			zap.Float64("load_skew", scheduleDecision.LoadSkew),
		)

		account := selection.Account
		if endpoint.IsGenerationRequest() {
			eligible, eligibilityReason, eligibilityErr := h.ensureGrokMediaAccountEligibility(requestCtx, account)
			if !eligible {
				mediaEligibilityRejected = true
				failedAccountIDs[account.ID] = struct{}{}
				reqLog.Warn("grok_media.account_eligibility_rejected",
					zap.Int64("account_id", account.ID),
					zap.String("reason", eligibilityReason),
					zap.Bool("probe_failed", eligibilityErr != nil),
				)
				if switchCount >= maxAccountSwitches {
					markOpsRoutingCapacityLimited(c)
					h.errorResponse(c, http.StatusServiceUnavailable, "grok_media_no_eligible_account", "No eligible Grok media accounts")
					return
				}
				switchCount++
				continue
			}
		}
		sessionHash = ensureOpenAIPoolModeSessionHash(sessionHash, account)
		setOpsSelectedAccount(c, account.ID, account.Platform)

		slotSessionHash := sessionHash
		if boundLookupAccountID > 0 {
			// Exact task-owner selection is authoritative; do not rewrite its
			// long-lived binding with the generic sticky-session TTL.
			slotSessionHash = ""
		}
		accountReleaseFunc, slotResult := h.acquireResponsesAccountSlot(c, apiKey.GroupID, slotSessionHash, selection, false, &streamStarted, reqLog)
		if slotResult == openAISlotAcquireProfitVetoed {
			// 媒体路径已显式豁免利润门（suppress 标记），此分支仅防御性兜底，
			// 同样受否决上限约束。
			if !recordOpenAIProfitVeto(failedAccountIDs, account.ID, &profitVetoCount) {
				h.handleOpenAIProfitVetoExhausted(c, streamStarted, reqLog, profitVetoCount)
				return
			}
			continue
		}
		if slotResult != openAISlotAcquireOK {
			return
		}

		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()
		writerSizeBeforeForward := c.Writer.Size()
		result, err := func() (*service.OpenAIForwardResult, error) {
			defer func() {
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
			}()
			if !isGrokVideoCreateEndpoint(endpoint) {
				return h.gatewayService.ForwardGrokMedia(requestCtx, c, account, endpoint, requestID, body, contentType)
			}
			return h.gatewayService.ForwardGrokMediaWithBeforeResponse(
				requestCtx,
				c,
				account,
				endpoint,
				requestID,
				body,
				contentType,
				func(result *service.OpenAIForwardResult) error {
					return persistGrokVideoCreateState(
						requestCtx,
						h,
						reqLog,
						c,
						apiKey,
						subject,
						account,
						result,
						requestModel,
						videoCreateStartedAt,
					)
				},
			)
		}()

		forwardDurationMs := time.Since(forwardStart).Milliseconds()
		upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
		responseLatencyMs := forwardDurationMs
		if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
			responseLatencyMs = forwardDurationMs - upstreamLatencyMs
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)

		if err != nil {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				if failoverClientGone(c) {
					reqLog.Info("grok_media.failover_aborted_client_disconnected",
						zap.Int64("account_id", account.ID),
						zap.Int("upstream_status", failoverErr.StatusCode),
					)
					return
				}
				if failoverErr.ShouldReportAccountScheduleFailure() {
					h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, grokMediaScheduleModel(account, routingModel, nil), false, nil)
				}
				if c.Writer.Size() != writerSizeBeforeForward {
					h.handleFailoverExhausted(c, failoverErr, true)
					return
				}
				if !failoverErr.ShouldRetryNextAccount() {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				if endpoint.IsVideoLookupRequest() {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				if failoverErr.RetryableOnSameAccount {
					retryLimit := account.GetPoolModeRetryCount()
					if sameAccountRetryCount[account.ID] < retryLimit {
						sameAccountRetryCount[account.ID]++
						reqLog.Warn("grok_media.pool_mode_same_account_retry",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
							zap.Int("retry_limit", retryLimit),
							zap.Int("retry_count", sameAccountRetryCount[account.ID]),
						)
						select {
						case <-requestCtx.Done():
							return
						case <-time.After(sameAccountRetryDelay):
						}
						continue
					}
				}
				h.gatewayService.RecordOpenAIAccountSwitch()
				failedAccountIDs[account.ID] = struct{}{}
				lastFailoverErr = failoverErr
				if switchCount >= maxAccountSwitches {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				switchCount++
				if h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
					h.handleFailoverExhausted(c, failoverErr, false)
					return
				}
				reqLog.Warn("grok_media.upstream_failover_switching",
					zap.Int64("account_id", account.ID),
					zap.Int("upstream_status", failoverErr.StatusCode),
					zap.Int("switch_count", switchCount),
					zap.Int("max_switches", maxAccountSwitches),
				)
				continue
			}
			h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, grokMediaScheduleModel(account, routingModel, nil), false, nil)
			if !service.IsResponseCommitted(c) && c.Writer.Size() == writerSizeBeforeForward {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			}
			reqLog.Warn("grok_media.forward_failed",
				zap.Int64("account_id", account.ID),
				zap.Error(err),
			)
			return
		}

		h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, grokMediaScheduleModel(account, routingModel, result), true, nil)
		if isGrokVideoCreateEndpoint(endpoint) && strings.TrimSpace(result.ResponseID) != "" {
			startGrokVideoCompletionObserver(
				h,
				c,
				requestCtx,
				reqLog,
				apiKey,
				subject,
				subscription,
				account,
				result.ResponseID,
				requestModel,
			)
		}
		// Status poll OR content download can observe official done+video.url.
		// Both paths share the same claim key so the customer is charged once.
		if endpoint == service.GrokMediaEndpointVideoStatus || endpoint == service.GrokMediaEndpointVideoContent {
			taskID := strings.TrimSpace(requestID)
			if billResult := prepareGrokVideoCompletionBilling(requestCtx, h, reqLog, apiKey, subject, taskID, result); billResult != nil {
				recordGrokMediaUsage(c, h, reqLog, apiKey, subject, subscription, account, billResult, billResult.Model, body, taskID)
			}
		} else if shouldRecordGrokMediaUsage(endpoint, requestModel, result) {
			recordGrokMediaUsage(c, h, reqLog, apiKey, subject, subscription, account, result, requestModel, body, requestID)
		}
		reqLog.Debug("grok_media.request_completed",
			zap.Int64("account_id", account.ID),
			zap.Int("switch_count", switchCount),
		)
		return
	}
}

func (h *OpenAIGatewayHandler) ensureGrokMediaAccountEligibility(ctx context.Context, account *service.Account) (bool, string, error) {
	if account == nil {
		return false, "missing_account", errors.New("grok media account is required")
	}
	eligible, reason := account.GrokMediaGenerationEligibility()
	if eligible || reason != "billing_unobserved" {
		return eligible, reason, nil
	}
	if h == nil || h.grokMediaEligibilityProber == nil {
		return false, "billing_probe_unavailable", errors.New("grok media eligibility probe is not configured")
	}
	return h.grokMediaEligibilityProber.ProbeMediaEligibility(ctx, account.ID)
}

func grokMediaRequiredCapability(endpoint service.GrokMediaEndpoint) service.OpenAIEndpointCapability {
	if endpoint.IsGenerationRequest() {
		return service.OpenAIEndpointCapabilityGrokMediaGeneration
	}
	return ""
}

func grokMediaScheduleModel(account *service.Account, routingModel string, result *service.OpenAIForwardResult) string {
	if result != nil && strings.TrimSpace(result.UpstreamModel) != "" {
		return result.UpstreamModel
	}
	if account == nil {
		return strings.TrimSpace(routingModel)
	}
	return account.GetMappedModel(routingModel)
}

func isGrokVideoCreateEndpoint(endpoint service.GrokMediaEndpoint) bool {
	switch endpoint {
	case service.GrokMediaEndpointVideosGenerations,
		service.GrokMediaEndpointVideosEdits,
		service.GrokMediaEndpointVideosExtensions:
		return true
	default:
		return false
	}
}

const (
	grokVideoCompletionObserverLifetime      = 15 * time.Minute
	grokVideoCompletionBillingMaxAttempts    = 3
	grokVideoCompletionBillingAttemptTimeout = 10 * time.Second
	grokVideoCompletionBillingRetryDelay     = time.Second
)

func persistGrokVideoCreateState(
	ctx context.Context,
	h *OpenAIGatewayHandler,
	reqLog *zap.Logger,
	c *gin.Context,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	account *service.Account,
	result *service.OpenAIForwardResult,
	requestModel string,
	createdAt string,
) error {
	if h == nil || h.gatewayService == nil || apiKey == nil || account == nil || result == nil {
		return errors.New("grok video create state dependencies are incomplete")
	}
	taskID := strings.TrimSpace(result.ResponseID)
	if taskID == "" {
		return errors.New("grok video create response is missing request id")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	pending := service.GrokVideoPendingBilling{
		AccountID:            account.ID,
		Model:                requestModel,
		BillingModel:         firstNonEmptyString(result.BillingModel, requestModel),
		UpstreamModel:        result.UpstreamModel,
		VideoResolution:      result.VideoResolution,
		VideoDurationSeconds: result.VideoDurationSeconds,
		OriginalModel:        clientRequestedModel(c, requestModel),
		// Wall-clock start for usage duration_ms: create accepted -> first done discovery.
		CreatedAt: createdAt,
	}
	if err := h.gatewayService.StoreGrokVideoPendingBilling(persistCtx, taskID, subject.UserID, apiKey.ID, pending); err != nil {
		reqLog.Warn("grok_media.store_video_pending_billing_failed_retrying",
			zap.Int64("account_id", account.ID),
			zap.String("request_id", taskID),
			zap.Error(err),
		)
		if retryErr := h.gatewayService.StoreGrokVideoPendingBilling(persistCtx, taskID, subject.UserID, apiKey.ID, pending); retryErr != nil {
			return fmt.Errorf("store grok video pending billing: %w", retryErr)
		}
	}

	// Keep the existing sticky owner key for the hot path. The pending snapshot
	// above already contains AccountID and is the authoritative fallback, so a
	// transient failure here cannot expose the task under another account.
	if err := h.gatewayService.BindGrokMediaVideoRequestAccount(
		persistCtx, apiKey.GroupID, taskID, subject.UserID, apiKey.ID, account.ID,
	); err != nil {
		reqLog.Warn("grok_media.bind_video_request_account_failed_using_pending_owner",
			zap.Int64("account_id", account.ID),
			zap.String("request_id", taskID),
			zap.Error(err),
		)
	}
	return nil
}

func startGrokVideoCompletionObserver(
	h *OpenAIGatewayHandler,
	c *gin.Context,
	requestCtx context.Context,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	subscription *service.UserSubscription,
	account *service.Account,
	taskID string,
	requestModel string,
) {
	if h == nil || h.gatewayService == nil || c == nil || apiKey == nil || account == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	taskID = strings.TrimSpace(taskID)
	usageSnapshot := captureGrokMediaUsageRecordSnapshot(c, apiKey, account, requestModel, nil, taskID)
	observerBase := context.Background()
	if requestCtx != nil {
		observerBase = usageRecordContext(requestCtx, observerBase)
	}

	go func() {
		observerCtx, cancel := context.WithTimeout(observerBase, grokVideoCompletionObserverLifetime)
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				reqLog.Error("grok_media.video_completion_observer_panic",
					zap.String("request_id", taskID),
					zap.Any("panic", recovered),
				)
			}
		}()
		err := h.gatewayService.ObserveGrokVideoCompletion(observerCtx, account, taskID, func(statusResult *service.OpenAIForwardResult) {
			recordObservedGrokVideoCompletion(
				observerCtx,
				h,
				reqLog,
				apiKey,
				subject,
				subscription,
				account,
				statusResult,
				usageSnapshot,
				taskID,
			)
		})
		if err != nil {
			reqLog.Warn("grok_media.video_completion_observer_stopped",
				zap.String("request_id", taskID),
				zap.Error(err),
			)
		}
	}()
}

func recordObservedGrokVideoCompletion(
	ctx context.Context,
	h *OpenAIGatewayHandler,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	subscription *service.UserSubscription,
	account *service.Account,
	statusResult *service.OpenAIForwardResult,
	usageSnapshot grokMediaUsageRecordSnapshot,
	taskID string,
) {
	recorded := retryGrokVideoCompletionBilling(
		ctx,
		grokVideoCompletionBillingMaxAttempts,
		grokVideoCompletionBillingAttemptTimeout,
		grokVideoCompletionBillingRetryDelay,
		func(attemptCtx context.Context) bool {
			billResult, preparation := prepareGrokVideoCompletionBillingWithStatus(
				attemptCtx, h, reqLog, apiKey, subject, taskID, statusResult,
			)
			switch preparation {
			case grokVideoBillingPreparationAlreadyClaimed, grokVideoBillingPreparationNotBillable:
				return true
			case grokVideoBillingPreparationRetryable:
				return false
			}
			prepareGrokVideoUsageResult(billResult, taskID)
			return recordGrokMediaUsageNow(
				attemptCtx, h, reqLog, apiKey, subject, subscription, account, billResult, usageSnapshot, taskID,
			) == nil
		})
	if recorded {
		return
	}
	reqLog.Warn("grok_media.video_completion_billing_retry_exhausted",
		zap.String("request_id", taskID),
		zap.Int("max_attempts", grokVideoCompletionBillingMaxAttempts),
	)
}

func retryGrokVideoCompletionBilling(
	ctx context.Context,
	maxAttempts int,
	attemptTimeout time.Duration,
	retryDelay time.Duration,
	attempt func(context.Context) bool,
) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if maxAttempts <= 0 || attempt == nil {
		return false
	}
	for i := 0; i < maxAttempts; i++ {
		attemptCtx := ctx
		cancel := func() {}
		if attemptTimeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, attemptTimeout)
		}
		succeeded := attempt(attemptCtx)
		cancel()
		if succeeded {
			return true
		}
		if i+1 >= maxAttempts {
			break
		}
		if !waitForGrokVideoBillingRetry(ctx, retryDelay) {
			return false
		}
	}
	return false
}

func waitForGrokVideoBillingRetry(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// shouldRecordGrokMediaUsage gates usage writes for immediate (image) generation.
// Async video create never bills here — status polling does on official
// status=done with video.url (docs.x.ai Video Generation).
// Status/content polls, empty model, and failed generations with zero billable
// image units never bill via this helper.
func shouldRecordGrokMediaUsage(endpoint service.GrokMediaEndpoint, requestModel string, result *service.OpenAIForwardResult) bool {
	if result == nil {
		return false
	}
	if isGrokVideoCreateEndpoint(endpoint) || endpoint.IsVideoLookupRequest() {
		return false
	}
	if !endpoint.IsGenerationRequest() || strings.TrimSpace(requestModel) == "" {
		return false
	}
	return result.ImageCount > 0
}

// prepareGrokVideoCompletionBilling claims one-shot billing for official done+video.url
// observations (status poll or content download). Duration/model prefer status body;
// resolution uses create-time request (status response does not document resolution).
type grokVideoBillingPreparation uint8

const (
	grokVideoBillingPreparationNotBillable grokVideoBillingPreparation = iota
	grokVideoBillingPreparationReady
	grokVideoBillingPreparationAlreadyClaimed
	grokVideoBillingPreparationRetryable
)

func prepareGrokVideoCompletionBilling(
	ctx context.Context,
	h *OpenAIGatewayHandler,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	taskRequestID string,
	statusResult *service.OpenAIForwardResult,
) *service.OpenAIForwardResult {
	result, _ := prepareGrokVideoCompletionBillingWithStatus(
		ctx, h, reqLog, apiKey, subject, taskRequestID, statusResult,
	)
	return result
}

func prepareGrokVideoCompletionBillingWithStatus(
	ctx context.Context,
	h *OpenAIGatewayHandler,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	taskRequestID string,
	statusResult *service.OpenAIForwardResult,
) (*service.OpenAIForwardResult, grokVideoBillingPreparation) {
	if h == nil || h.gatewayService == nil || apiKey == nil || statusResult == nil {
		return nil, grokVideoBillingPreparationNotBillable
	}
	// Forward already set VideoCount only when status=done && video.url (official).
	if statusResult.VideoCount <= 0 {
		return nil, grokVideoBillingPreparationNotBillable
	}
	taskRequestID = strings.TrimSpace(firstNonEmptyString(taskRequestID, statusResult.ResponseID))
	if taskRequestID == "" {
		return nil, grokVideoBillingPreparationNotBillable
	}
	// Load create-time snapshot before claim so we can fail-closed without burning the claim
	// when Redis lost pending and status cannot price the job.
	pending, loadErr := h.gatewayService.LoadGrokVideoPendingBilling(ctx, taskRequestID, subject.UserID, apiKey.ID)
	if loadErr != nil {
		reqLog.Warn("grok_media.video_pending_billing_load_failed", zap.String("request_id", taskRequestID), zap.Error(loadErr))
		return nil, grokVideoBillingPreparationRetryable
	}
	if pending == nil {
		// Status omits resolution; without pending we would silently default to 480p and underbill.
		// Allow billing only when official status carries duration (still may default resolution).
		if statusResult.VideoDurationSeconds <= 0 {
			reqLog.Error("grok_media.video_billing_skipped_missing_pending",
				zap.String("request_id", taskRequestID),
				zap.String("reason", "no create-time snapshot and status has no video.duration"),
			)
			return nil, grokVideoBillingPreparationRetryable
		}
		reqLog.Error("grok_media.video_billing_without_pending",
			zap.String("request_id", taskRequestID),
			zap.Int("status_duration_seconds", statusResult.VideoDurationSeconds),
			zap.String("note", "resolution falls back to default 480p; investigate pending store failures"),
		)
	}
	claimed, err := h.gatewayService.ClaimGrokVideoBilling(ctx, taskRequestID, subject.UserID, apiKey.ID)
	if err != nil {
		reqLog.Warn("grok_media.video_billing_claim_failed", zap.String("request_id", taskRequestID), zap.Error(err))
		return nil, grokVideoBillingPreparationRetryable
	}
	if !claimed {
		// Redis claim ownership is only a hot-path guard. A caller can win the
		// claim and then fail before the durable usage transaction commits; in
		// that window another observer must still be allowed to reach
		// RecordUsage. The PostgreSQL usage_billing_dedup transaction is the
		// authoritative idempotency boundary, so proceeding here either records
		// the missing charge or becomes a no-op when the other caller succeeded.
		reqLog.Debug("grok_media.video_billing_claim_held_using_durable_dedup", zap.String("request_id", taskRequestID))
	}
	// Re-merge with pending: resolution is request-only; model/duration fill gaps.
	merged := *statusResult
	if pending != nil {
		if strings.TrimSpace(merged.Model) == "" {
			merged.Model = firstNonEmptyString(pending.BillingModel, pending.Model, pending.OriginalModel)
		}
		if strings.TrimSpace(merged.BillingModel) == "" {
			merged.BillingModel = firstNonEmptyString(pending.BillingModel, pending.Model, merged.Model)
		}
		if strings.TrimSpace(merged.UpstreamModel) == "" {
			merged.UpstreamModel = pending.UpstreamModel
		}
		// Official status omits resolution — always prefer create request.
		if strings.TrimSpace(pending.VideoResolution) != "" {
			merged.VideoResolution = pending.VideoResolution
		}
		if merged.VideoDurationSeconds <= 0 {
			merged.VideoDurationSeconds = pending.VideoDurationSeconds
		}
		if strings.TrimSpace(merged.ResponseID) == "" {
			merged.ResponseID = taskRequestID
		}
	}
	if strings.TrimSpace(merged.Model) == "" {
		merged.Model = "grok-imagine-video"
	}
	if strings.TrimSpace(merged.BillingModel) == "" {
		merged.BillingModel = merged.Model
	}
	// Always force durable task id so usage_billing_dedup survives multi-poll +
	// context-local request ids (do not prefer empty-only fill).
	merged.RequestID = service.StableGrokVideoBillingRequestID(firstNonEmptyString(merged.ResponseID, taskRequestID))
	merged.ResponseID = firstNonEmptyString(merged.ResponseID, taskRequestID)
	merged.VideoCount = 1
	// Pure video: do not keep legacy ImageCount (avoids image-path heuristics).
	merged.ImageCount = 0
	// Official default resolution is 480p when the create request omitted it.
	merged.VideoResolution = service.NormalizeVideoBillingResolutionOrDefault(merged.VideoResolution)
	// Official default duration is 8s when neither status nor create provided it.
	merged.VideoDurationSeconds = service.NormalizeVideoBillingDurationSecondsOrDefault(merged.VideoDurationSeconds)
	// E2E latency for async video: create accept → this discovery of done+url.
	// Bill on discovery (status/content), not after further client polls; duration
	// must not be only the single discovery hop (~hundreds of ms).
	if pending != nil {
		if e2e := service.GrokVideoE2EDuration(pending.CreatedAt, time.Now()); e2e > 0 {
			merged.Duration = e2e
		}
	}
	return &merged, grokVideoBillingPreparationReady
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

type grokMediaUsageRecordSnapshot struct {
	ParentContext      context.Context
	InboundEndpoint    string
	UpstreamEndpoint   string
	UserAgent          string
	IPAddress          string
	SessionID          string
	RequestPayloadHash string
	QuotaPlatform      string
	RequestModel       string
	ChannelUsageFields service.ChannelUsageFields
}

func captureGrokMediaUsageRecordSnapshot(
	c *gin.Context,
	apiKey *service.APIKey,
	account *service.Account,
	requestModel string,
	body []byte,
	requestID string,
) grokMediaUsageRecordSnapshot {
	payloadForHash := body
	if len(payloadForHash) == 0 && strings.TrimSpace(requestID) != "" {
		payloadForHash = []byte(strings.TrimSpace(requestID))
	}
	parentCtx := context.Background()
	if c != nil && c.Request != nil {
		// Retain only request ids used by the usage worker. Do not keep the full
		// Gin/request object (including Authorization and large media bodies) for
		// the lifetime of an asynchronous video observer.
		parentCtx = usageRecordContext(c.Request.Context(), context.Background())
	}
	snapshot := grokMediaUsageRecordSnapshot{
		ParentContext:      parentCtx,
		RequestPayloadHash: service.HashUsageRequestPayload(payloadForHash),
		RequestModel:       requestModel,
		ChannelUsageFields: service.ChannelUsageFields{ChannelMappedModel: requestModel},
	}
	if c == nil {
		return snapshot
	}
	snapshot.UserAgent = c.GetHeader("User-Agent")
	snapshot.IPAddress = ip.GetClientIP(c)
	snapshot.SessionID = service.ExtractClientSessionID(c)
	snapshot.InboundEndpoint = GetInboundEndpoint(c)
	if account != nil {
		snapshot.UpstreamEndpoint = GetUpstreamEndpoint(c, account.Platform)
	}
	if c.Request != nil {
		snapshot.QuotaPlatform = service.QuotaPlatform(c.Request.Context(), apiKey)
	}
	// Composite/public aliases come from request context; billing model selection
	// remains unchanged because BillingModelSource is not overridden here.
	snapshot.ChannelUsageFields.OriginalModel = clientRequestedModel(c, requestModel)
	return snapshot
}

func prepareGrokVideoUsageResult(result *service.OpenAIForwardResult, taskID string) {
	if result == nil || result.VideoCount <= 0 {
		return
	}
	if stable := service.StableGrokVideoBillingRequestID(firstNonEmptyString(result.ResponseID, taskID)); stable != "" {
		result.RequestID = stable
	}
}

func recordGrokMediaUsage(
	c *gin.Context,
	h *OpenAIGatewayHandler,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	subscription *service.UserSubscription,
	account *service.Account,
	result *service.OpenAIForwardResult,
	requestModel string,
	body []byte,
	requestID string,
) {
	// Async video: force durable task request id and release claim if billing fails.
	videoTaskID := ""
	if result != nil && result.VideoCount > 0 {
		videoTaskID = strings.TrimSpace(firstNonEmptyString(requestID, result.ResponseID))
		prepareGrokVideoUsageResult(result, videoTaskID)
		// Prefer task id hash for payload fingerprint stability across status/content.
		if len(body) == 0 {
			requestID = videoTaskID
		}
	}
	snapshot := captureGrokMediaUsageRecordSnapshot(c, apiKey, account, requestModel, body, requestID)
	submitGrokMediaUsageRecord(h, reqLog, apiKey, subject, subscription, account, result, snapshot, videoTaskID)
}

func submitGrokMediaUsageRecord(
	h *OpenAIGatewayHandler,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	subscription *service.UserSubscription,
	account *service.Account,
	result *service.OpenAIForwardResult,
	snapshot grokMediaUsageRecordSnapshot,
	videoTaskID string,
) {
	h.submitOpenAIUsageRecordTask(snapshot.ParentContext, result, func(ctx context.Context) {
		_ = recordGrokMediaUsageNow(
			ctx, h, reqLog, apiKey, subject, subscription, account, result, snapshot, videoTaskID,
		)
	})
}

func recordGrokMediaUsageNow(
	ctx context.Context,
	h *OpenAIGatewayHandler,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	subject middleware2.AuthSubject,
	subscription *service.UserSubscription,
	account *service.Account,
	result *service.OpenAIForwardResult,
	snapshot grokMediaUsageRecordSnapshot,
	videoTaskID string,
) error {
	err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
		Result:             result,
		APIKey:             apiKey,
		User:               apiKey.User,
		Account:            account,
		Subscription:       subscription,
		InboundEndpoint:    snapshot.InboundEndpoint,
		UpstreamEndpoint:   snapshot.UpstreamEndpoint,
		UserAgent:          snapshot.UserAgent,
		IPAddress:          snapshot.IPAddress,
		RequestPayloadHash: snapshot.RequestPayloadHash,
		APIKeyService:      h.apiKeyService,
		QuotaPlatform:      snapshot.QuotaPlatform,
		SessionID:          snapshot.SessionID,
		ChannelUsageFields: snapshot.ChannelUsageFields,
	})
	if err == nil {
		return nil
	}
	if videoTaskID != "" {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if releaseErr := h.gatewayService.ReleaseGrokVideoBilling(releaseCtx, videoTaskID, subject.UserID, apiKey.ID); releaseErr != nil {
			reqLog.Warn("grok_media.video_billing_claim_release_failed",
				zap.String("request_id", videoTaskID),
				zap.Error(releaseErr),
			)
		}
		cancel()
	}
	logger.L().With(
		zap.String("component", "handler.openai_gateway.grok_media"),
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("model", snapshot.RequestModel),
		zap.Int64("account_id", account.ID),
	).Error("grok_media.record_usage_failed", zap.Error(err))
	reqLog.Debug("grok_media.record_usage_failed", zap.Error(err))
	return err
}
