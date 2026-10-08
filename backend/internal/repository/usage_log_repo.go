package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	gocache "github.com/patrickmn/go-cache"
)

const rawUsageLogModelColumn = "model"

// rawUsageLogModelColumn preserves the exact stored usage_logs.model semantics for direct filters.
// Historical rows may contain upstream/billing model values, while newer rows store requested_model.
// Requested/upstream/mapping analytics must use resolveModelDimensionExpression instead.

// usageLogSuccessFilterUL 用于把"失败请求 usage log"（tokens=0、cost=0、不计费的占位记录）
// 从统计性聚合中排除，避免污染 Dashboard / 用量拆分等指标。
//
// schema 中没有 success bool 列；新增列要做迁移，风险大；这里用 actual_cost > 0 作为代理：
// 任何成功落账的请求都会产生 actual_cost（包括 token 计费、纯图片 token 计费、按次/按图计费），
// 反之 failed-request usage log 的 actual_cost 为 0。
// 早期版本用 4 项 token 和 > 0 判定会把"按次/按图计费"与"image_output_tokens 独立计费"的纯图片
// 请求误判为失败，导致这部分请求从用量统计里消失，故改用 actual_cost。
// 配合 `FROM usage_logs ul` JOIN 查询使用。
const usageLogSuccessFilterUL = "ul.actual_cost > 0"

// usageLogEffectivePlatformExpr 用于按"有效平台"维度聚合 usage_logs：
// 优先取请求实际走的分组 platform，若分组未设置 platform 再 fallback 到 account.platform。
// Composite groups are a routing layer, so platform analytics must use the
// resolved concrete account platform instead of grouping spend under "composite".
// 配套要求查询里 LEFT JOIN groups g ON g.id = ul.group_id 与 LEFT JOIN accounts a ON a.id = ul.account_id。
const usageLogEffectivePlatformExpr = "CASE WHEN g.platform = 'composite' THEN a.platform ELSE COALESCE(NULLIF(g.platform,''), a.platform) END"

// dateFormatWhitelist 将 granularity 参数映射为 PostgreSQL TO_CHAR 格式字符串，防止外部输入直接拼入 SQL
var dateFormatWhitelist = map[string]string{
	"hour":  "YYYY-MM-DD HH24:00",
	"day":   "YYYY-MM-DD",
	"week":  "IYYY-IW",
	"month": "YYYY-MM",
}

// safeDateFormat 根据白名单获取 dateFormat，未匹配时返回默认值
func safeDateFormat(granularity string) string {
	if f, ok := dateFormatWhitelist[granularity]; ok {
		return f
	}
	return "YYYY-MM-DD"
}

// appendRawUsageLogModelWhereCondition keeps direct model filters on the raw model column for backward
// compatibility with historical rows. Requested/upstream analytics must use
// resolveModelDimensionExpression instead.
func appendRawUsageLogModelWhereCondition(conditions []string, args []any, model string) ([]string, []any) {
	if strings.TrimSpace(model) == "" {
		return conditions, args
	}
	conditions = append(conditions, fmt.Sprintf("%s = $%d", rawUsageLogModelColumn, len(args)+1))
	args = append(args, model)
	return conditions, args
}

func appendUsageLogBillingModeWhereCondition(conditions []string, args []any, billingMode string) ([]string, []any) {
	return appendUsageLogBillingModeWhereConditionWithAlias(conditions, args, billingMode, "")
}

func appendUsageLogBillingModeWhereConditionWithAlias(conditions []string, args []any, billingMode string, alias string) ([]string, []any) {
	mode := strings.TrimSpace(billingMode)
	if mode == "" {
		return conditions, args
	}
	column := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	placeholder := fmt.Sprintf("$%d", len(args)+1)
	switch service.BillingMode(mode) {
	case service.BillingModeImage:
		conditions = append(conditions, fmt.Sprintf("(%s = %s OR ((%s IS NULL OR %s = '') AND COALESCE(%s, 0) > 0))", column("billing_mode"), placeholder, column("billing_mode"), column("billing_mode"), column("image_count")))
	case service.BillingModeVideo:
		conditions = append(conditions, fmt.Sprintf("%s = %s", column("billing_mode"), placeholder))
	case service.BillingModeToken:
		conditions = append(conditions, fmt.Sprintf("(%s = %s OR ((%s IS NULL OR %s = '') AND COALESCE(%s, 0) <= 0))", column("billing_mode"), placeholder, column("billing_mode"), column("billing_mode"), column("image_count")))
	default:
		conditions = append(conditions, fmt.Sprintf("%s = %s", column("billing_mode"), placeholder))
	}
	args = append(args, mode)
	return conditions, args
}

func appendUsageLogBillingModeQueryFilter(query string, args []any, billingMode string, alias string) (string, []any) {
	conditions, args := appendUsageLogBillingModeWhereConditionWithAlias(nil, args, billingMode, alias)
	if len(conditions) == 0 {
		return query, args
	}
	return query + " AND " + conditions[0], args
}

func appendUsageLogModelWhereCondition(conditions []string, args []any, model string, source string) ([]string, []any) {
	if strings.TrimSpace(source) == "" {
		return appendRawUsageLogModelWhereCondition(conditions, args, model)
	}
	if strings.TrimSpace(model) == "" {
		return conditions, args
	}
	conditions = append(conditions, fmt.Sprintf("%s = $%d", resolveModelDimensionExpression(source), len(args)+1))
	args = append(args, model)
	return conditions, args
}

// appendRawUsageLogModelQueryFilter keeps direct model filters on the raw model column for backward
// compatibility with historical rows. Requested/upstream analytics must use
// resolveModelDimensionExpression instead.
func appendRawUsageLogModelQueryFilter(query string, args []any, model string) (string, []any) {
	if strings.TrimSpace(model) == "" {
		return query, args
	}
	query += fmt.Sprintf(" AND %s = $%d", rawUsageLogModelColumn, len(args)+1)
	args = append(args, model)
	return query, args
}

func appendUsageLogModelQueryFilter(query string, args []any, model string, source string) (string, []any) {
	if strings.TrimSpace(source) == "" {
		return appendRawUsageLogModelQueryFilter(query, args, model)
	}
	if strings.TrimSpace(model) == "" {
		return query, args
	}
	query += fmt.Sprintf(" AND %s = $%d", resolveModelDimensionExpression(source), len(args)+1)
	args = append(args, model)
	return query, args
}

type usageLogRepository struct {
	client *dbent.Client
	sql    sqlExecutor
	db     *sql.DB

	createBatchOnce     sync.Once
	createBatchCh       chan usageLogCreateRequest
	bestEffortBatchOnce sync.Once
	bestEffortBatchCh   chan usageLogBestEffortRequest
	bestEffortRecent    *gocache.Cache
	schedulingQuality   *gocache.Cache
}

func NewUsageLogRepository(client *dbent.Client, sqlDB *sql.DB) service.UsageLogRepository {
	return newUsageLogRepositoryWithSQL(client, sqlDB)
}

func newUsageLogRepositoryWithSQL(client *dbent.Client, sqlq sqlExecutor) *usageLogRepository {
	// 使用 scanSingleRow 替代 QueryRowContext，保证 ent.Tx 作为 sqlExecutor 可用。
	repo := &usageLogRepository{client: client, sql: sqlq}
	if db, ok := sqlq.(*sql.DB); ok {
		repo.db = db
	}
	repo.bestEffortRecent = gocache.New(usageLogBestEffortRecentTTL, time.Minute)
	repo.schedulingQuality = gocache.New(30*time.Second, 5*time.Minute)
	return repo
}

func (r *usageLogRepository) GetGroupModelAccountQuality(ctx context.Context, groupID int64, model string, since time.Time) (map[int64]service.GroupModelAccountQuality, error) {
	cacheKey := fmt.Sprintf("%d:%s:%d", groupID, model, since.Truncate(time.Minute).Unix())
	if cached, ok := r.schedulingQuality.Get(cacheKey); ok {
		if quality, ok := cached.(map[int64]service.GroupModelAccountQuality); ok {
			return quality, nil
		}
	}
	rows, err := r.sql.QueryContext(ctx, `
		WITH failure_requests AS (
			SELECT
				COALESCE(NULLIF((event->>'account_id')::bigint, 0), errors.account_id) AS account_id,
				COALESCE(NULLIF(errors.request_id, ''), 'error:' || errors.id::text) AS request_key,
				MAX(errors.created_at) AS last_failure_at
			FROM ops_error_logs errors
			LEFT JOIN LATERAL jsonb_array_elements(
				CASE WHEN jsonb_typeof(errors.upstream_errors) = 'array'
				     THEN errors.upstream_errors ELSE '[]'::jsonb END
			) event ON true
			WHERE errors.group_id = $1
			  AND COALESCE(NULLIF(BTRIM(errors.requested_model), ''), errors.model) = $2
			  AND errors.created_at >= $3
			  AND errors.error_phase IN ('upstream', 'account_auth')
			  AND COALESCE(errors.is_count_tokens, false) = false
			  AND COALESCE(errors.error_owner, '') NOT IN ('client', 'user')
			  AND COALESCE(event->>'scope', '') NOT IN ('request', 'provider', 'proxy')
			GROUP BY 1, 2
		), successes AS (
			SELECT usage.account_id,
			       COUNT(*) FILTER (WHERE NOT EXISTS (
			           SELECT 1 FROM failure_requests failure
			           WHERE failure.account_id = usage.account_id
			             AND failure.request_key = usage.request_id
			       )) AS successes,
			       MAX(usage.created_at) FILTER (WHERE NOT EXISTS (
			           SELECT 1 FROM failure_requests failure
			           WHERE failure.account_id = usage.account_id AND failure.request_key = usage.request_id
			       )) AS last_success_at,
			       percentile_cont(0.95) WITHIN GROUP (
			           ORDER BY CASE WHEN usage.stream AND usage.first_token_ms > 0
			                         THEN usage.first_token_ms ELSE usage.duration_ms END
			       ) FILTER (WHERE NOT EXISTS (
			           SELECT 1 FROM failure_requests failure
			           WHERE failure.account_id = usage.account_id AND failure.request_key = usage.request_id
			       )) AS latency_ms
			FROM usage_logs usage
			WHERE usage.group_id = $1
			  AND COALESCE(NULLIF(BTRIM(usage.requested_model), ''), usage.model) = $2
			  AND usage.created_at >= $3
			  AND usage.duration_ms > 0
			  AND (usage.total_cost > 0 OR usage.actual_cost > 0
			       OR usage.input_tokens > 0 OR usage.output_tokens > 0
			       OR usage.cache_creation_tokens > 0 OR usage.cache_read_tokens > 0
			       OR usage.image_count > 0 OR usage.image_output_tokens > 0)
			GROUP BY usage.account_id
		), failures AS (
			SELECT account_id, COUNT(*) AS failures,
			       COUNT(*) FILTER (WHERE last_failure_at >= NOW() - INTERVAL '15 minutes') AS recent_failures,
			       MAX(last_failure_at) AS last_failure_at
			FROM failure_requests
			WHERE account_id > 0
			GROUP BY account_id
		)
		SELECT COALESCE(successes.account_id, failures.account_id),
		       COALESCE(successes.successes, 0), COALESCE(failures.failures, 0),
		       COALESCE(successes.latency_ms, 0), COALESCE(failures.recent_failures, 0),
		       successes.last_success_at, failures.last_failure_at
		FROM successes FULL OUTER JOIN failures USING (account_id)
		`, groupID, model, since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	quality := make(map[int64]service.GroupModelAccountQuality)
	for rows.Next() {
		var accountID int64
		var sample service.GroupModelAccountQuality
		if err := rows.Scan(&accountID, &sample.Successes, &sample.Failures, &sample.LatencyMS,
			&sample.RecentFailures, &sample.LastSuccessAt, &sample.LastFailureAt); err != nil {
			return nil, err
		}
		quality[accountID] = sample
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	r.schedulingQuality.SetDefault(cacheKey, quality)
	return quality, nil
}

func buildWhere(conditions []string) string {
	if len(conditions) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conditions, " AND ")
}

func appendRequestTypeOrStreamWhereCondition(conditions []string, args []any, requestType *int16, stream *bool) ([]string, []any) {
	if requestType != nil {
		condition, conditionArgs := buildRequestTypeFilterCondition(len(args)+1, *requestType)
		conditions = append(conditions, condition)
		args = append(args, conditionArgs...)
		return conditions, args
	}
	if stream != nil {
		conditions = append(conditions, fmt.Sprintf("stream = $%d", len(args)+1))
		args = append(args, *stream)
	}
	return conditions, args
}

func appendRequestTypeOrStreamQueryFilter(query string, args []any, requestType *int16, stream *bool) (string, []any) {
	if requestType != nil {
		condition, conditionArgs := buildRequestTypeFilterCondition(len(args)+1, *requestType)
		query += " AND " + condition
		args = append(args, conditionArgs...)
		return query, args
	}
	if stream != nil {
		query += fmt.Sprintf(" AND stream = $%d", len(args)+1)
		args = append(args, *stream)
	}
	return query, args
}

func appendNativeCompactionV2WhereCondition(conditions []string, args []any, nativeCompactionV2 *bool, alias string) ([]string, []any) {
	if nativeCompactionV2 == nil {
		return conditions, args
	}
	column := "native_compaction_v2"
	if alias != "" {
		column = alias + "." + column
	}
	conditions = append(conditions, fmt.Sprintf("%s = $%d", column, len(args)+1))
	args = append(args, *nativeCompactionV2)
	return conditions, args
}

func appendNativeCompactionV2QueryFilter(query string, args []any, nativeCompactionV2 *bool, alias string) (string, []any) {
	conditions, args := appendNativeCompactionV2WhereCondition(nil, args, nativeCompactionV2, alias)
	if len(conditions) == 0 {
		return query, args
	}
	return query + " AND " + conditions[0], args
}

// buildRequestTypeFilterCondition 在 request_type 过滤时兼容 legacy 字段，避免历史数据漏查。
func buildRequestTypeFilterCondition(startArgIndex int, requestType int16) (string, []any) {
	return buildRequestTypeFilterConditionWithAlias(startArgIndex, requestType, "")
}

func buildRequestTypeFilterConditionWithAlias(startArgIndex int, requestType int16, alias string) (string, []any) {
	normalized := service.RequestTypeFromInt16(requestType)
	requestTypeArg := int16(normalized)
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	switch normalized {
	case service.RequestTypeSync:
		return fmt.Sprintf("(%srequest_type = $%d OR (%srequest_type = %d AND %sstream = FALSE AND %sopenai_ws_mode = FALSE))", prefix, startArgIndex, prefix, int16(service.RequestTypeUnknown), prefix, prefix), []any{requestTypeArg}
	case service.RequestTypeStream:
		return fmt.Sprintf("(%srequest_type = $%d OR (%srequest_type = %d AND %sstream = TRUE AND %sopenai_ws_mode = FALSE))", prefix, startArgIndex, prefix, int16(service.RequestTypeUnknown), prefix, prefix), []any{requestTypeArg}
	case service.RequestTypeWSV2:
		return fmt.Sprintf("(%srequest_type = $%d OR (%srequest_type = %d AND %sopenai_ws_mode = TRUE))", prefix, startArgIndex, prefix, int16(service.RequestTypeUnknown), prefix), []any{requestTypeArg}
	default:
		return fmt.Sprintf("%srequest_type = $%d", prefix, startArgIndex), []any{requestTypeArg}
	}
}
