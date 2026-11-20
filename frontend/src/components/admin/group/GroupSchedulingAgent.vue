<template>
  <details class="rounded-lg border border-gray-200 p-4 dark:border-dark-600" @toggle="open">
    <summary class="cursor-pointer font-medium">分组智能调度与常驻智能体</summary>
    <div class="mt-4 space-y-4">
      <p v-if="error" class="text-sm text-red-600" role="alert">{{ error }}</p>
      <p v-if="message" class="text-sm text-green-600" role="status">{{ message }}</p>
      <p v-if="!status && !error" class="text-sm">读取分组策略…</p>
      <template v-if="status">
        <p class="input-hint">新会话按主用、二用和兜底顺序选择；健康老会话保留账号。使用上游池后，先在同池内寻找支持请求模型的成员，再使用下一池。</p>
        <fieldset :disabled="busy" class="space-y-3">
          <label class="text-sm">调度层级
            <select class="input" aria-label="调度层级" :value="poolMode ? 'pools' : 'accounts'" @change="changeMode">
              <option value="accounts">账号顺序</option>
              <option value="pools">上游池主备</option>
            </select>
          </label>
          <template v-if="poolMode">
            <div v-for="(pool, index) in pools" :key="pool.id" class="space-y-2 rounded border border-gray-200 p-3 dark:border-dark-600">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-sm font-medium">{{ index === 0 ? '主力池' : index === 1 ? '二用池' : `兜底池 ${index - 1}` }}</span>
                <input v-model.trim="pool.name" class="input min-w-32 flex-1" placeholder="例如 秋 API" aria-label="上游池名称" maxlength="100" />
                <button type="button" class="btn btn-secondary btn-sm" :disabled="index === 0" aria-label="上移池" @click="move(pools, index, -1)">↑</button>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="index === pools.length - 1" aria-label="下移池" @click="move(pools, index, 1)">↓</button>
                <button type="button" class="btn btn-secondary btn-sm" @click="pools.splice(index, 1)">删除池</button>
              </div>
              <div v-for="(id, position) in pool.accounts" :key="id" class="flex flex-wrap items-center gap-2 text-sm">
                <span class="min-w-32 flex-1">{{ position + 1 }}. {{ accountLabel(id) }}</span>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="position === 0" aria-label="上移池成员" @click="move(pool.accounts, position, -1)">↑</button>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="position === pool.accounts.length - 1" aria-label="下移池成员" @click="move(pool.accounts, position, 1)">↓</button>
                <button type="button" class="btn btn-secondary btn-sm" @click="removeMember(pool, id)">移除</button>
              </div>
              <select class="input" aria-label="添加池成员" :value="''" @change="addAccount({ ids: pool.accounts }, $event)">
                <option value="">添加本组账号到此池…</option>
                <option v-for="account in status.accounts.filter(a => !pools.some(p => p.accounts.includes(a.id)))" :key="account.id" :value="account.id">{{ accountLabel(account.id) }}</option>
              </select>
              <details>
                <summary class="cursor-pointer text-sm">池内精确模型顺序（可选）</summary>
                <p class="input-hint">默认按上述成员顺序跳过不支持模型的账号。模型规则仅使用所列成员；空列表表示此池不承接该模型。</p>
                <div v-for="(ids, model) in pool.models" :key="model" class="my-2 space-y-1 border-t border-gray-200 pt-2 dark:border-dark-600">
                  <div class="flex items-center gap-2 text-sm"><span class="flex-1 break-all">{{ model }}</span><button type="button" class="btn btn-secondary btn-sm" @click="delete pool.models![model]">删除模型规则</button></div>
                  <div v-for="(id, position) in ids" :key="id" class="flex flex-wrap items-center gap-2 text-sm">
                    <span class="flex-1">{{ position + 1 }}. {{ accountLabel(id) }}</span>
                    <button type="button" class="btn btn-secondary btn-sm" :disabled="position === 0" aria-label="上移模型成员" @click="move(ids, position, -1)">↑</button>
                    <button type="button" class="btn btn-secondary btn-sm" :disabled="position === ids.length - 1" aria-label="下移模型成员" @click="move(ids, position, 1)">↓</button>
                    <button type="button" class="btn btn-secondary btn-sm" @click="ids.splice(position, 1)">移除</button>
                  </div>
                  <select class="input" aria-label="添加模型成员" :value="''" @change="addAccount({ ids }, $event)">
                    <option value="">添加池内成员…</option>
                    <option v-for="id in pool.accounts.filter(id => !ids.includes(id))" :key="id" :value="id">{{ accountLabel(id) }}</option>
                  </select>
                </div>
                <div class="mt-2 flex flex-wrap gap-2"><input v-model.trim="poolModelDrafts[pool.id]" class="input flex-1" placeholder="精确模型名" aria-label="池内精确模型名" /><button type="button" class="btn btn-secondary btn-sm" @click="addPoolModel(pool)">添加池内模型规则</button></div>
              </details>
            </div>
            <button type="button" class="btn btn-secondary btn-sm" @click="addPool">添加上游池</button>
            <p class="input-hint">每个账号在本组只能属于一个池。未加入任何池的账号不参与该组调度。</p>
          </template>
          <template v-else>
          <div v-for="(route, index) in routes" :key="route.key" class="space-y-2 rounded border border-gray-200 p-3 dark:border-dark-600">
            <div class="flex flex-wrap items-center gap-2">
              <span v-if="index === 0" class="text-sm font-medium">默认顺序</span>
              <template v-else>
                <input v-model.trim="route.model" class="input flex-1" placeholder="精确模型名，例如 gpt-6-astra" aria-label="精确模型名" />
                <button type="button" class="btn btn-secondary btn-sm" @click="routes.splice(index, 1)">删除模型</button>
              </template>
            </div>
            <div v-for="(id, position) in route.ids" :key="id" class="flex flex-wrap items-center gap-2 text-sm">
              <span class="min-w-12 text-gray-500">{{ position === 0 ? '主用' : position === 1 ? '二用' : `兜底 ${position - 1}` }}</span>
              <span class="min-w-32 flex-1">{{ accountLabel(id) }}</span>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="position === 0" aria-label="上移账号" @click="move(route.ids, position, -1)">↑</button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="position === route.ids.length - 1" aria-label="下移账号" @click="move(route.ids, position, 1)">↓</button>
              <button type="button" class="btn btn-secondary btn-sm" @click="route.ids.splice(position, 1)">移除</button>
            </div>
            <select class="input" aria-label="添加分组账号" :value="''" @change="addAccount(route, $event)">
              <option value="">添加账号…</option>
              <option v-for="account in status.accounts.filter(a => !route.ids.includes(a.id))" :key="account.id" :value="account.id">{{ accountLabel(account.id) }}</option>
            </select>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" @click="routes.push({ key: ++routeKey, model: '', ids: [] })">添加精确模型</button>
          </template>
          <label class="block text-sm">智能体观察模型
            <textarea v-model="observeModels" class="input" rows="2" placeholder="每行一个精确模型名；池内模型规则也会自动纳入观察" aria-label="智能体观察模型" />
          </label>
          <div class="grid gap-2 sm:grid-cols-3">
            <label class="text-sm">智能体权限
              <select v-model="policy.agent_mode" class="input">
                <option value="off">暂停</option>
                <option value="observe">仅观察</option>
                <option value="auto">允许按模型自动调度</option>
              </select>
            </label>
            <label class="text-sm">本组每日调用上限
              <input v-model.number="policy.daily_calls" type="number" min="1" max="144" class="input" />
            </label>
            <label class="text-sm">允许的最短等待（毫秒）
              <input v-model.number="policy.min_wait_ms" type="number" min="100" max="2000" class="input" />
            </label>
          </div>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-primary btn-sm" @click="save">保存调度策略</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!status.policy.agent_mode || status.policy.agent_mode === 'off'" @click="operate('run')">立即分析</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="status.run.state !== 'applied' && !Object.keys(status.run.routes ?? {}).length" @click="operate('revoke')">撤回智能体调整</button>
            <button type="button" class="btn btn-secondary btn-sm" @click="refresh">刷新状态</button>
          </div>
        </fieldset>
        <div v-if="status.run.state" class="space-y-2 rounded border border-gray-200 p-3 text-sm dark:border-dark-600">
          <p>{{ stateLabel(status.run.state) }} · {{ date(status.run.at) }} · 今日 {{ status.run.calls }} 次 · 本轮输入 {{ status.run.input_tokens }} / 输出 {{ status.run.output_tokens }} Token</p>
          <p v-if="status.run.decision?.reason" class="break-words">{{ status.run.decision.reason }}</p>
          <p v-for="(inspection, index) in status.run.inspections" :key="index" class="break-words">
            取证：{{ inspection.model }} · {{ inspection.effort || '常用档位' }} · 最近 {{ inspection.hours }} 小时 · {{ inspection.reason }}
          </p>
          <p v-if="status.run.decision?.action === 'defer_member'">{{ status.run.decision.model }} · 暂缓 {{ accountLabel(status.run.decision.account_id) }} 承接新会话 · 到期 {{ date(status.run.expires) }} · 已应用 {{ status.run.applied_requests ?? 0 }} 次</p>
          <p v-if="status.run.decision?.action === 'shorten_capacity_wait'">{{ status.run.decision.model }} · {{ accountLabel(status.run.decision.account_id) }} · {{ status.run.decision.wait_ms }} ms<span v-if="status.run.state === 'applied'"> · 到期 {{ date(status.run.expires) }} · 已应用 {{ status.run.applied_requests ?? 0 }} 次 &middot; 备用成功 {{ status.run.completed_attempts ?? 0 }} / 失败 {{ status.run.failed_attempts ?? 0 }}</span></p>
          <div v-for="(route, model) in status.run.routes" :key="model" class="border-t border-gray-200 pt-2 dark:border-dark-600">
            <p class="break-words font-medium">{{ model }}</p>
            <p v-if="route.account_id" class="break-words">优先承接新会话：{{ accountLabel(route.account_id) }} · {{ date(route.selected_at) }} · {{ route.reason }}</p>
            <p v-if="route.trial_id" class="break-words">少量试用：{{ accountLabel(route.trial_id) }} · 截至 {{ date(route.trial_until) }} · {{ route.trial_reason }}</p>
          </div>
          <details v-if="status.run.evidence?.length || status.history?.length">
            <summary class="cursor-pointer">观测数据与最近动作</summary>
            <div v-for="item in status.run.evidence" :key="`${item.model}:${item.account_id}`" class="mt-1 break-words">
              {{ item.model }} · {{ item.pool_name ? `${item.pool_name}池 · ` : '' }}{{ accountLabel(item.account_id) }} · 成功 {{ item.successes }} / 失败 {{ item.failures }} · 延迟 {{ Math.round(item.first_event_ms) }} ms · 等待超时 {{ item.full_slots }} 次<span v-if="item.cache_ratio != null"> &middot; 缓存命中 {{ (item.cache_ratio * 100).toFixed(1) }}%</span><span v-if="item.speed?.output_tps"> &middot; {{ item.speed.output_tps.toFixed(1) }} Token/s</span>
              <span v-if="item.current"> · 本轮基准</span><span v-if="item.trial"> · 试用对象</span>
              <p v-for="window in item.recent_windows" :key="`${window.age}:${window.effort}:${window.context_bucket}`" class="text-xs text-gray-500">
                {{ window.age === 0 ? '最近 30 分钟' : '前一段 30 分钟' }} · {{ window.effort || '未指定思考强度' }} · 上下文档位 {{ window.context_bucket }} · {{ window.samples }} 条 · 首输出 P50 {{ Math.round(window.p50_ms) }} / P95 {{ Math.round(window.p95_ms) }} ms
              </p>
            </div>
            <p v-for="(run, index) in status.history" :key="index" class="mt-1 break-words">
              {{ date(run.at) }} · {{ stateLabel(run.state) }} · {{ run.decision?.model }} · 应用 {{ run.applied_requests ?? 0 }} 次 · {{ run.decision?.reason }}
            </p>
          </details>
        </div>
        <details class="rounded border border-gray-200 p-3 dark:border-dark-600" @toggle="openConnection">
          <summary class="cursor-pointer font-medium">智能体连接设置（所有分组共用）</summary>
          <fieldset v-if="connection" :disabled="busy" class="mt-3 grid gap-2 sm:grid-cols-2">
            <label class="text-sm">模型 API 地址<input v-model.trim="connection.url" class="input" placeholder="https://provider.example/v1" /></label>
            <label class="text-sm">模型名称<input v-model.trim="connection.model" class="input" /></label>
            <label class="text-sm">API Key<input v-model="connection.api_key" type="password" autocomplete="new-password" class="input" :placeholder="connection.key_configured ? '已设置，留空保留' : '请输入 Key'" /></label>
            <label class="text-sm">思考等级<input v-model.trim="connection.effort" class="input" placeholder="例如 low，留空用模型默认值" /></label>
            <label class="text-sm">总每日调用上限<input v-model.number="connection.daily_calls" type="number" min="1" max="500" class="input" /></label>
            <label class="text-sm">常规分析间隔（分钟）<input v-model.number="connection.interval_minutes" type="number" min="1" max="1440" class="input" /></label>
            <div class="flex flex-wrap gap-2 sm:col-span-2">
              <button type="button" class="btn btn-primary btn-sm" @click="saveConnection(false)">保存连接</button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!connection.key_configured" @click="testConnection">测试已保存连接</button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!connection.key_configured" @click="saveConnection(true)">清除 Key</button>
            </div>
          </fieldset>
        </details>
      </template>
    </div>
  </details>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { schedulingAgentAPI, type SchedulingConnection, type SchedulingPolicy, type SchedulingStatus, type SchedulingPool } from '@/api/admin/schedulingAgent'

const props = defineProps<{ groupId: number }>()
const status = ref<SchedulingStatus>()
const connection = ref<SchedulingConnection>()
const routes = ref<{ key: number; model: string; ids: number[] }[]>([])
const policy = ref<SchedulingPolicy>({ accounts: [], models: {}, agent_mode: 'off', daily_calls: 24, min_wait_ms: 500 })
const pools = ref<SchedulingPool[]>([])
const poolMode = ref(false)
const poolModelDrafts = ref<Record<string, string>>({})
const observeModels = ref('')
const busy = ref(false)
const error = ref('')
const message = ref('')
let routeKey = 0

const date = (value: string) => new Date(value).toLocaleString()
const stateLabel = (state: string) => ({ reassessed: '已重新评估', reverted_after_failures: '备用失败，已恢复基础等待', observed: '已观察', applied: '已应用临时动作', route_selected: '已更新该模型的优先线路', route_trial: '已开始少量试用', trial_stopped: '已停止接收试用会话', expired: '动作已到期', revoked: '动作已撤回', insufficient_evidence: '样本不足', budget_exhausted: '今日调用额度已用完', calling: '正在分析', connected: '连接测试通过', model_unreachable: '模型无法连接', invalid_model_decision: '模型返回的决策格式无效', invalid_inspection: '取证范围无效或达到上限' }[state] ?? state)
function accountLabel(id: number) {
  const account = status.value?.accounts.find(a => a.id === id)
  if (!account) return `#${id}（已移出分组）`
  const binding = account.upstream_group ? `${account.upstream_group}${account.rate !== undefined ? ` · ${account.rate}x` : ''}` : '上游分组未核验'
  return `${account.name} #${id}${account.site ? ` · ${account.site}` : ''} · ${binding}${account.available === false ? ' · 暂不可调度' : ''}`
}
function hydrate(value: SchedulingStatus) {
  status.value = value
  policy.value = { ...value.policy, agent_mode: value.policy.agent_mode || 'off', daily_calls: value.policy.daily_calls || 24, min_wait_ms: value.policy.min_wait_ms || 500 }
  routes.value = [{ key: ++routeKey, model: '', ids: [...(value.policy.accounts ?? [])] }, ...Object.entries(value.policy.models ?? {}).map(([model, ids]) => ({ key: ++routeKey, model, ids: [...ids] }))]
  pools.value = (value.policy.pools ?? []).map(pool => ({ ...pool, accounts: [...pool.accounts], models: Object.fromEntries(Object.entries(pool.models ?? {}).map(([model, ids]) => [model, [...ids]])) }))
  poolMode.value = pools.value.length > 0
  observeModels.value = (value.policy.observe_models ?? []).join('\n')
}
async function request(fn: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  message.value = ''
  try { await fn() } catch (err) { error.value = (err as Error).message || '操作失败' } finally { busy.value = false }
}
function open(event: Event) {
  if ((event.target as HTMLDetailsElement).open && !status.value) void refresh()
}
function refresh() { return request(async () => { hydrate(await schedulingAgentAPI.status(props.groupId)) }) }
function openConnection(event: Event) {
  if (!(event.target as HTMLDetailsElement).open || connection.value) return
  void request(async () => {
    const value = await schedulingAgentAPI.connection()
    connection.value = { ...value, daily_calls: value.daily_calls || 48, interval_minutes: value.interval_minutes || 30 }
  })
}
function move<T>(ids: T[], position: number, offset: number) {
  const id = ids[position]
  ids.splice(position, 1)
  ids.splice(position + offset, 0, id)
}
function changeMode(event: Event) {
  poolMode.value = (event.target as HTMLSelectElement).value === 'pools'
  if (poolMode.value && !pools.value.length) addPool()
}
function addPool() {
  pools.value.push({ id: crypto.randomUUID(), name: '', accounts: [], models: {} })
}
function removeMember(pool: SchedulingPool, id: number) {
  pool.accounts = pool.accounts.filter(member => member !== id)
  for (const model of Object.keys(pool.models ?? {})) pool.models![model] = pool.models![model].filter(member => member !== id)
}
function addPoolModel(pool: SchedulingPool) {
  const model = poolModelDrafts.value[pool.id]
  if (!model || /[\s*?]/.test(model)) { error.value = '请输入不含空格或通配符的精确模型名'; return }
  if (!pool.models) pool.models = {}
  if (Object.prototype.hasOwnProperty.call(pool.models, model)) { error.value = '此池已有该模型规则'; return }
  pool.models[model] = pool.accounts.filter(id => {
    const models = status.value?.accounts.find(account => account.id === id)?.models
    return models?.includes(model) || models?.some(pattern => pattern.includes('*') || pattern.includes('?'))
  })
  poolModelDrafts.value[pool.id] = ''
  error.value = ''
}
function addAccount(route: { ids: number[] }, event: Event) {
  const select = event.target as HTMLSelectElement
  const id = Number(select.value)
  if (id && !route.ids.includes(id)) route.ids.push(id)
  select.value = ''
}
function save() {
  return request(async () => {
    const entries = routes.value.slice(1).map(route => [route.model, route.ids] as const)
    if (!poolMode.value && (entries.some(([model, ids]) => !model || !ids.length) || new Set(entries.map(([model]) => model)).size !== entries.length)) {
      throw new Error('每条规则都需要不同的精确模型名和至少一个账号')
    }
    if (poolMode.value && (!pools.value.length || pools.value.some(pool => !pool.name || !pool.accounts.length))) throw new Error('每个上游池都需要名称和至少一个成员')
    hydrate(await schedulingAgentAPI.save(props.groupId, { ...policy.value, pools: poolMode.value ? pools.value : [], observe_models: [...new Set(observeModels.value.split(/\s+/).filter(Boolean))], accounts: poolMode.value ? [] : routes.value[0].ids, models: poolMode.value ? {} : Object.fromEntries(entries) }))
    message.value = '调度策略已保存'
  })
}
function operate(operation: 'run' | 'revoke') {
  return request(async () => { status.value = await schedulingAgentAPI[operation](props.groupId) })
}
function saveConnection(clear: boolean) {
  return request(async () => {
    if (!connection.value) return
    connection.value = await schedulingAgentAPI.saveConnection({ ...connection.value, clear_key: clear })
    message.value = clear ? '模型 Key 已清除' : '连接设置已保存'
  })
}
function testConnection() {
  return request(async () => {
    const run = await schedulingAgentAPI.testConnection()
    if (run.state !== 'connected') throw new Error(stateLabel(run.state))
    message.value = `${stateLabel(run.state)} · 输入 ${run.input_tokens} / 输出 ${run.output_tokens} Token`
  })
}
</script>
