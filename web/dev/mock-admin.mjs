/* global structuredClone */
/**
 * Mock routes of the Accounts, Usage, Settings and Roles screens (contract §4–§5): account writes and sign-in,
 * providers, usage summary and history, settings PATCH with field errors, project memory, hooks (the test answers
 * 501 not_implemented, as the daemon does until its hook runner lands) and roles. Imported by mock-daemon.mjs,
 * which owns the shared data (accounts, settings, projects) and the HTTP plumbing.
 *
 * Sign-in simulation, as the daemon does it (internal/signin): starting, then in_terminal once the sign-in window
 * is "open" (with the daemon's message), then done after 6 s (codex: 8 s). No backend reaches open_url or
 * waiting_code, so POST /accounts/{id}/login/code answers 409 bad_state like the daemon.
 */

const MIN = 60_000
const HOUR = 60 * MIN
const DAY = 24 * HOUR
const now = () => new Date().toISOString()
const ago = (ms) => new Date(Date.now() - ms).toISOString()

const ID = /^[a-z0-9][a-z0-9-]{0,39}$/
const TIERS = ['fast', 'standard', 'strong', 'max']
const EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max']
export const HOOK_EVENTS = [
  'task.created',
  'task.started',
  'task.blocked',
  'task.unblocked',
  'task.done',
  'task.failed',
  'task.reviewed',
  'task.cancelled',
  'task.takeover',
  'task.handback',
  'account.exhausted',
  'account.needs_login',
  'daemon.started',
  'daemon.reattached',
]

function hash8(text) {
  let h = 2166136261
  for (const ch of text) h = Math.imul(h ^ ch.charCodeAt(0), 16777619) >>> 0
  return h.toString(16).padStart(8, '0')
}

/**
 * Memory text as Go's memory.Store.Replace stores it: whitespace only becomes empty, anything else ends with a
 * newline, so the dashboard meets the same text here as against the real daemon.
 */
const normaliseMemory = (text) =>
  text.trim() === '' ? '' : text.endsWith('\n') ? text : `${text}\n`

/** A project key as the daemon builds it: slug of the folder name plus 8 hex of the path. */
const projectKey = (cwd, name) =>
  `${name.toLowerCase().replace(/[^a-z0-9._-]+/g, '-')}-${hash8(cwd.toLowerCase())}`

export function createAdminRoutes(ctx) {
  const { accounts, settings, projects, broadcast, readBody, send, HttpError } = ctx
  /** Every /hooks route answers 501, as a daemon without hooks does. */
  const noHooks = ctx.noHooks === true
  const invalid = (message, field) =>
    new HttpError(400, 'invalid_argument', message, field ? { field } : undefined)
  const notFound = (what) => new HttpError(404, 'not_found', `${what} not found`)
  const conflict = (message) => new HttpError(409, 'conflict', message)
  const isObject = (v) => typeof v === 'object' && v !== null && !Array.isArray(v)

  // -------------------------------------------------------------------------------------------------------------
  // Accounts

  /** The status an account returns to when it is enabled again. */
  const baseStatus = new Map(accounts.map((a) => [a.id, a.status === 'disabled' ? 'ok' : a.status]))
  const logins = new Map()
  let accountCounter = 0

  const ordered = () =>
    [...accounts].sort((a, b) => b.priority - a.priority || a.label.localeCompare(b.label))

  function accountOr404(id) {
    const found = accounts.find((a) => a.id === id)
    if (!found) throw notFound(`account ${id}`)
    return found
  }

  function changed(account, status) {
    if (status) {
      baseStatus.set(account.id, status)
      account.status = account.enabled ? status : 'disabled'
    }
    account.updatedAt = now()
    broadcast('account.updated', account)
    return account
  }

  function freshQuota(account) {
    if (account.kind === 'api-key') return { source: 'passive', fetchedAt: now(), groups: [] }
    const groups =
      account.backend === 'agy'
        ? [
            {
              name: 'gemini-pro',
              windows: [{ window: '5h', remaining: 0.97, resetAt: ago(-4 * HOUR) }],
            },
            {
              name: 'gemini-flash',
              windows: [{ window: '5h', remaining: 1, resetAt: ago(-4 * HOUR) }],
            },
          ]
        : [
            {
              name: 'sonnet',
              windows: [{ window: '5h', remaining: 0.88, resetAt: ago(-3 * HOUR) }],
            },
          ]
    return { source: 'active', fetchedAt: now(), plan: account.quota.plan ?? 'Pro', groups }
  }

  function setLogin(account, session) {
    logins.set(account.id, session)
    broadcast('account.login', session)
  }

  function finishLogin(account, ok, message) {
    const session = { accountId: account.id, state: ok ? 'done' : 'failed', message }
    setLogin(account, session)
    account.signingIn = false
    if (ok) {
      account.email = `${account.label.toLowerCase().replace(/\W+/g, '.')}@example.test`
      account.quota = freshQuota(account)
      changed(account, 'ok')
    } else changed(account)
    setTimeout(() => logins.get(account.id) === session && logins.delete(account.id), 60_000)
  }

  function startLogin(account) {
    if (account.kind === 'api-key') throw invalid('API-key accounts do not sign in; set a key')
    account.signingIn = true
    changed(account)
    setLogin(account, { accountId: account.id, state: 'starting' })
    const later = (ms, fn) =>
      setTimeout(() => {
        const current = logins.get(account.id)
        if (current && current.state !== 'done' && current.state !== 'failed') fn()
      }, ms)
    later(700, () =>
      setLogin(account, {
        accountId: account.id,
        state: 'in_terminal',
        message: 'Finish signing in in the terminal window that opened.',
      }),
    )
    later(account.backend === 'codex' ? 8000 : 6000, () => finishLogin(account, true))
    return logins.get(account.id)
  }

  function checkLabel(label) {
    if (typeof label !== 'string' || !label.trim()) throw invalid('label is required', 'label')
    if (label.length > 80) throw invalid('label is longer than 80 characters', 'label')
  }

  function checkPriority(priority) {
    if (!Number.isInteger(priority) || priority < -1000 || priority > 1000) {
      throw invalid('priority must be a whole number from -1000 to 1000', 'priority')
    }
  }

  async function accountRoutes(req, res, method, parts) {
    if (parts.length === 1) {
      if (method === 'GET') return send(res, 200, ordered())
      if (method !== 'POST') return false
      const body = await readBody(req, [
        'backend',
        'kind',
        'label',
        'provider',
        'enabled',
        'priority',
      ])
      if (!['agy', 'claude', 'codex'].includes(body.backend))
        throw invalid('unknown backend', 'backend')
      if (!['isolated', 'system', 'api-key'].includes(body.kind))
        throw invalid('unknown kind', 'kind')
      checkLabel(body.label)
      if (body.priority !== undefined) checkPriority(body.priority)
      if (body.kind === 'system') {
        if (body.backend !== 'agy') throw invalid('only Antigravity has a system account', 'kind')
        if (accounts.some((a) => a.id === 'system'))
          throw conflict('the system account exists already')
      }
      if (body.kind === 'api-key') {
        if (body.backend === 'agy') throw invalid('Antigravity has no API-key accounts', 'kind')
        const provider = providers.find((p) => p.id === body.provider)
        if (!provider) throw invalid('choose a provider', 'provider')
        if (provider.backend !== body.backend)
          throw invalid('the provider is for another backend', 'provider')
      } else if (body.provider !== undefined)
        throw invalid('only API-key accounts have a provider', 'provider')
      const id =
        body.kind === 'system' ? 'system' : `acc-${Date.now().toString(36)}${++accountCounter}`
      const status =
        body.kind === 'api-key' ? 'no_key' : body.kind === 'system' ? 'new' : 'needs_login'
      const account = {
        id,
        backend: body.backend,
        kind: body.kind,
        label: body.label.trim(),
        enabled: body.enabled ?? true,
        priority: body.priority ?? 0,
        ...(body.provider ? { provider: body.provider } : {}),
        status,
        busy: 0,
        maxConcurrent: settings.maxConcurrentPerAccount,
        resting: false,
        signingIn: false,
        quota: { source: body.kind === 'api-key' ? 'passive' : 'active', groups: [] },
        models: [],
        createdAt: now(),
        updatedAt: now(),
      }
      accounts.push(account)
      baseStatus.set(id, status)
      if (!account.enabled) account.status = 'disabled'
      broadcast('account.updated', account)
      if (body.kind === 'system') {
        setTimeout(() => {
          account.quota = freshQuota(account)
          changed(account, 'ok')
        }, 3000)
      }
      return send(res, 201, account)
    }

    const account = accountOr404(parts[1])
    const sub = parts.slice(2).join('/')
    if (sub === '' && method === 'PATCH') {
      const body = await readBody(req, ['label', 'enabled', 'priority'])
      if (body.label !== undefined) {
        checkLabel(body.label)
        account.label = body.label.trim()
      }
      if (body.priority !== undefined) {
        checkPriority(body.priority)
        account.priority = body.priority
      }
      if (body.enabled !== undefined) {
        if (typeof body.enabled !== 'boolean')
          throw invalid('enabled must be true or false', 'enabled')
        account.enabled = body.enabled
        account.status = body.enabled ? baseStatus.get(account.id) : 'disabled'
      }
      return send(res, 200, changed(account))
    }
    if (sub === '' && method === 'DELETE') {
      if (account.busy > 0) {
        throw new HttpError(409, 'bad_state', `${account.label} is running ${account.busy} task(s)`)
      }
      accounts.splice(accounts.indexOf(account), 1)
      logins.delete(account.id)
      broadcast('account.deleted', { id: account.id })
      return send(res, 204)
    }
    if (method === 'POST' && sub === 'refresh') {
      await readBody(req, [])
      return send(res, 200, refresh(account))
    }
    if (sub === 'login') {
      if (method === 'GET') {
        const session = logins.get(account.id)
        if (!session) throw notFound(`sign-in of ${account.id}`)
        return send(res, 200, session)
      }
      if (method === 'POST') {
        await readBody(req, [])
        return send(res, 200, startLogin(account))
      }
    }
    if (method === 'POST' && sub === 'login/code') {
      const body = await readBody(req, ['code'])
      const session = logins.get(account.id)
      if (!session || session.state !== 'waiting_code') {
        throw new HttpError(409, 'bad_state', 'no sign-in is waiting for a code')
      }
      if (typeof body.code !== 'string' || !body.code.trim())
        throw invalid('code is required', 'code')
      if (body.code.trim() === 'fail') finishLogin(account, false, 'The code was not accepted.')
      else finishLogin(account, true)
      return send(res, 200, logins.get(account.id))
    }
    if (method === 'POST' && sub === 'logout') {
      await readBody(req, [])
      delete account.email
      account.quota = {
        source: 'active',
        groups: [],
        error: { kind: 'needs_login', message: 'Signed out.', at: now() },
      }
      return send(res, 200, changed(account, 'needs_login'))
    }
    if (sub === 'key') {
      if (account.kind !== 'api-key') throw invalid('only API-key accounts have a key')
      if (method === 'PUT') {
        const body = await readBody(req, ['key'])
        if (typeof body.key !== 'string' || body.key.trim().length < 8) {
          throw invalid('the key is too short', 'key')
        }
        account.keyHint = `…${body.key.trim().slice(-4)}`
        account.quota = freshQuota(account)
        return send(res, 200, changed(account, 'ok'))
      }
      if (method === 'DELETE') {
        delete account.keyHint
        return send(res, 200, changed(account, 'no_key'))
      }
    }
    return false
  }

  function refresh(account) {
    if (
      account.kind === 'api-key' ||
      account.status === 'needs_login' ||
      account.status === 'no_plan'
    ) {
      account.quota = { ...account.quota, fetchedAt: now() }
      return changed(account)
    }
    if (account.status === 'error' || account.status === 'new') {
      const fresh = freshQuota(account)
      account.quota = {
        ...fresh,
        groups: account.quota.groups.length ? account.quota.groups : fresh.groups,
      }
      return changed(account, 'ok')
    }
    account.quota = { ...account.quota, fetchedAt: now() }
    return changed(account)
  }

  // -------------------------------------------------------------------------------------------------------------
  // Providers: the shapes of internal/api (Provider, ProviderPreset, ProviderInput), checked roughly like
  // account.Provider.Validate and the daemon's applyProvider. The allow-list follows the contract:
  // - `models` is the allow-list exactly as last set, never the tier models;
  // - `allowedModels` is the pattern enforced for it (exactPattern of the list); a hand-written pattern that is
  //   not such a list comes with `models: []`;
  // - both empty means any model, and tier models are always allowed on top;
  // - `models` in a body replaces the list (`[]` clears it); `allowedModels` sets a pattern (models wins when a
  //   body has both, the mock's choice).

  const NO_TIERS = { fast: '', standard: '', strong: '', max: '' }
  const presets = [
    preset('anthropic', 'Anthropic', 'claude', '', { official: true }),
    preset('deepseek', 'DeepSeek', 'claude', 'https://api.deepseek.com/anthropic'),
    preset('moonshot', 'Moonshot Kimi', 'claude', 'https://api.moonshot.ai/anthropic'),
    preset('zai', 'Z.ai GLM', 'claude', 'https://api.z.ai/api/anthropic'),
    preset('openrouter', 'OpenRouter', 'claude', 'https://openrouter.ai/api'),
    preset('custom-anthropic', 'Custom (Anthropic API)', 'claude', ''),
    preset('openai', 'OpenAI', 'codex', 'https://api.openai.com/v1', { wireApi: 'responses' }),
    preset('groq', 'Groq', 'codex', 'https://api.groq.com/openai/v1', { wireApi: 'chat' }),
    preset('ollama', 'Ollama (local)', 'codex', 'http://localhost:11434/v1', { wireApi: 'chat' }),
    preset('custom-openai', 'Custom (OpenAI API)', 'codex', '', { wireApi: 'chat' }),
  ]
  const providers = [
    fromPreset('anthropic', 'anthropic', { models: ['claude-sonnet-5', 'claude-opus-5'] }),
    // A list plus a tier model outside it: the tier model is allowed on top, and stays out of `models`.
    fromPreset('deepseek', 'deepseek', {
      models: ['deepseek-v4', 'deepseek-r2'],
      tiers: { fast: 'deepseek-chat' },
    }),
    // A hand-written pattern with tier models: `models` is [].
    fromPreset('zai', 'zai', {
      allowedModels: '^glm-',
      tiers: { fast: 'glm-5-air', standard: 'glm-5', strong: 'glm-5', max: 'glm-5' },
    }),
    fromPreset('openai', 'openai', { models: ['gpt-5.5-codex', 'gpt-5.5-mini'] }),
  ]

  function preset(id, name, backend, baseUrl, extra = {}) {
    return { id, name, backend, baseUrl, enabled: true, models: [], official: false, ...extra }
  }

  function fromPreset(id, presetId, { models, allowedModels, tiers } = {}) {
    const p = presets.find((x) => x.id === presetId)
    const at = now()
    const created = {
      ...p,
      id,
      preset: presetId,
      models: [],
      accounts: 0,
      allowedModels: '',
      tiers: { ...NO_TIERS, ...tiers },
      createdAt: at,
      updatedAt: at,
    }
    setAllowList(created, models, allowedModels)
    return created
  }

  /** Like the daemon's exactPattern: an anchored pattern of exactly these models ('' for none). */
  function exactPattern(models) {
    if (!models.length) return ''
    return `^(?:${models.map((m) => m.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})$`
  }

  /** The models an exact pattern names, or null for a hand-written pattern. */
  function listOf(pattern) {
    const exact = /^\^\(\?:(.+)\)\$$/.exec(pattern)
    if (!exact) return null
    const parts = exact[1].split(/(?<!\\)\|/)
    if (parts.some((part) => /(^|[^\\])[.*+?^${}()|[\]]/.test(part))) return null
    return parts.map((part) => part.replace(/\\(.)/g, '$1'))
  }

  /** Sets the allow-list from a list (models) or a pattern (allowedModels); both undefined leaves it. */
  function setAllowList(p, models, allowedModels) {
    if (models !== undefined) {
      p.models = [...models]
      p.allowedModels = exactPattern(models)
    } else if (allowedModels !== undefined) {
      p.allowedModels = allowedModels
      p.models = listOf(allowedModels) ?? []
    }
  }

  const providerView = (p) => ({
    ...p,
    accounts: accounts.filter((a) => a.provider === p.id).length,
  })

  const PROVIDER_FIELDS = [
    'id',
    'name',
    'backend',
    'baseUrl',
    'enabled',
    'models',
    'preset',
    'wireApi',
    'allowedModels',
    'tiers',
    'accounts',
  ]

  /** Applies a ProviderInput to p (applyProvider in the daemon), then validates the result. */
  function applyProvider(p, body) {
    for (const key of ['name', 'baseUrl', 'enabled', 'wireApi'])
      if (body[key] !== undefined) p[key] = body[key]
    if (body.tiers !== undefined) p.tiers = { ...NO_TIERS, ...body.tiers }
    if (body.models !== undefined) {
      if (
        !Array.isArray(body.models) ||
        body.models.some((m) => typeof m !== 'string' || !m.trim() || /\s/.test(m))
      ) {
        throw invalid('models are names without spaces', 'models')
      }
    }
    if (body.allowedModels !== undefined && body.models === undefined) {
      if (typeof body.allowedModels !== 'string')
        throw invalid('allowedModels must be text', 'allowedModels')
      try {
        new RegExp(body.allowedModels)
      } catch {
        throw invalid('allowedModels is not a valid regular expression', 'allowedModels')
      }
    }
    setAllowList(p, body.models, body.allowedModels)
    if (typeof p.name !== 'string' || p.name.length > 60) {
      throw invalid('name is at most 60 characters', 'name')
    }
    p.name = p.name.trim() || p.id
    if (typeof p.enabled !== 'boolean') throw invalid('enabled must be true or false', 'enabled')
    if (p.backend === 'claude' && p.wireApi) {
      throw invalid('wireApi is only for codex providers', 'wireApi')
    }
    if (p.backend === 'codex') {
      p.wireApi ||= 'chat'
      if (!['chat', 'responses'].includes(p.wireApi)) {
        throw invalid(`wireApi ${String(p.wireApi)} (want chat or responses)`, 'wireApi')
      }
    }
    if (p.official) {
      p.baseUrl = ''
    } else {
      let url
      try {
        url = new URL(p.baseUrl)
      } catch {
        throw invalid('baseUrl is required for a provider that is not official', 'baseUrl')
      }
      const local = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
      if (url.protocol !== 'https:' && !(url.protocol === 'http:' && local)) {
        throw invalid('baseUrl must use https (or http for localhost)', 'baseUrl')
      }
    }
    p.updatedAt = now()
  }

  async function providerRoutes(req, res, method, parts) {
    if (parts.length === 1) {
      if (method === 'GET') return send(res, 200, { items: providers.map(providerView), presets })
      if (method !== 'POST') return false
      const body = await readBody(req, PROVIDER_FIELDS)
      const id = typeof body.id === 'string' ? body.id.trim() : ''
      if (!ID.test(id)) throw invalid('the id uses a-z, 0-9 and -, at most 40 characters', 'id')
      if (providers.some((p) => p.id === id)) throw conflict(`provider ${id} exists already`)
      let created = { ...preset(id, id, '', ''), allowedModels: '', tiers: { ...NO_TIERS } }
      if (body.preset) {
        if (!presets.some((p) => p.id === body.preset)) {
          throw invalid(`unknown provider preset "${String(body.preset)}"`, 'preset')
        }
        created = fromPreset(id, body.preset)
      }
      if (body.backend !== undefined) {
        if (created.backend && created.backend !== body.backend) {
          const why = `preset ${created.preset} serves ${created.backend}, not ${body.backend}`
          throw invalid(why, 'backend')
        }
        created.backend = body.backend
      }
      if (!['claude', 'codex'].includes(created.backend)) {
        throw invalid('backend is required (claude or codex)', 'backend')
      }
      applyProvider(created, body)
      created.createdAt = created.updatedAt
      providers.push(created)
      broadcast('providers.changed', { changedAt: now() })
      return send(res, 201, providerView(created))
    }
    const found = providers.find((p) => p.id === parts[1])
    if (!found) throw notFound(`provider ${parts[1]}`)
    if (method === 'PATCH') {
      const body = await readBody(req, PROVIDER_FIELDS)
      if (body.id !== undefined && body.id !== found.id) {
        throw invalid("a provider's id cannot change", 'id')
      }
      if (body.backend !== undefined && body.backend !== found.backend) {
        throw invalid("a provider's backend cannot change", 'backend')
      }
      if (body.preset !== undefined && body.preset !== (found.preset ?? '')) {
        throw invalid("a provider's preset cannot change", 'preset')
      }
      const next = structuredClone(found)
      applyProvider(next, body)
      Object.assign(found, next)
      broadcast('providers.changed', { changedAt: now() })
      return send(res, 200, providerView(found))
    }
    if (method === 'DELETE') {
      const used = providerView(found).accounts
      if (used > 0) throw conflict(`${found.name} is used by ${used} account(s)`)
      providers.splice(providers.indexOf(found), 1)
      broadcast('providers.changed', { changedAt: now() })
      return send(res, 204)
    }
    return false
  }

  // -------------------------------------------------------------------------------------------------------------
  // Usage

  let useed = 7
  const urand = () => (useed = (useed * 1103515245 + 12345) % 2147483648) / 2147483648
  const localDate = (ms) => {
    const d = new Date(ms)
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  }
  const TASK_TITLES = [
    'Fix the checkout total rounding',
    'Add pagination to the orders API',
    'Write tests for the cart service',
    'Refactor the auth middleware',
    'Translate the settings page',
    'Speed up the product search',
    'Upgrade the payment SDK',
  ]

  function totals(days, factor) {
    const tasks = Math.round(days * 6 * factor)
    return {
      tokens: Math.round(days * 410_000 * factor),
      costUsd: Math.round(days * 3.1 * factor * 100) / 100,
      hasCost: true,
      tasks,
      turns: Math.round(tasks * 2.4),
      attempts: Math.round(tasks * 2.7),
    }
  }

  function summary(days) {
    useed = 7 + days
    const until = Date.now()
    const since = until - days * DAY
    const daily = []
    for (let i = days - 1; i >= 0; i--) {
      const weekend = [0, 6].includes(new Date(until - i * DAY).getDay())
      const scale = weekend ? 0.25 : 0.6 + urand() * 0.9
      daily.push({
        date: localDate(until - i * DAY),
        tokens: Math.round(410_000 * scale),
        costUsd: Math.round(3.1 * scale * 100) / 100,
        tasks: Math.round(6 * scale),
      })
    }
    const projectRows = projects.map((p, index) => {
      const share = [0.38, 0.24, 0.18, 0.12, 0.08][index] ?? 0.05
      const taskCount = Math.max(1, Math.round(days * 6 * share))
      const items = Array.from({ length: Math.min(taskCount, 5) }, (_, t) => {
        const tokens = Math.round(days * 410_000 * share * (0.4 / (t + 1)))
        const account = accounts[(index + t) % accounts.length]
        const turns = 1 + ((t + index) % 4)
        return {
          id: `t-${index}-${t}`,
          title: TASK_TITLES[(index * 3 + t) % TASK_TITLES.length],
          status: t === 0 ? 'running' : 'done',
          backend: account.backend,
          tokens,
          costUsd: Math.round(tokens * 0.0000075 * 100) / 100,
          durationMs: (8 + t * 5) * MIN,
          lastAt: ago((t + 1) * 5 * HOUR),
          accountId: account.id,
          account: account.label,
          turns,
          items: Array.from({ length: Math.min(turns, 5) }, (_, n) => ({
            n: n + 1,
            tokens: Math.round(tokens / turns),
            costUsd: Math.round((tokens / turns) * 0.0000075 * 100) / 100,
            durationMs: (3 + n) * MIN,
            attempts: n === 0 ? 2 : 1,
            at: ago((t + 1) * 5 * HOUR + n * 20 * MIN),
            accountId: account.id,
            account: account.label,
            model: account.backend === 'agy' ? 'gemini-3.8-flash' : 'claude-sonnet-5',
          })),
          more: 0,
        }
      })
      return {
        key: p.key,
        name: p.name,
        tokens: Math.round(days * 410_000 * share),
        costUsd: Math.round(days * 3.1 * share * 100) / 100,
        durationMs: Math.round(days * 90 * share) * MIN,
        tasks: taskCount,
        turns: Math.round(taskCount * 2.4),
        items,
        more: Math.max(0, taskCount - items.length),
      }
    })
    const accountRows = accounts.map((a, index) => {
      const share = [0.42, 0.21, 0.15, 0.08, 0.06, 0.04, 0.02, 0.01, 0.01][index] ?? 0.005
      const tokens = Math.round(days * 410_000 * share)
      return {
        id: a.id,
        label: a.label,
        backend: a.backend,
        tokens,
        costUsd: Math.round(tokens * 0.0000075 * 100) / 100,
        attempts: Math.round(days * 16 * share),
        durationMs: Math.round(days * 90 * share) * MIN,
        models:
          a.backend === 'agy'
            ? [
                {
                  model: 'gemini-3.8-flash',
                  tokens: Math.round(tokens * 0.7),
                  costUsd: 0,
                  attempts: 9,
                  durationMs: 40 * MIN,
                },
                {
                  model: 'gemini-3.8-pro',
                  tokens: Math.round(tokens * 0.3),
                  costUsd: 0,
                  attempts: 3,
                  durationMs: 22 * MIN,
                },
              ]
            : [
                {
                  model: 'claude-sonnet-5',
                  tokens,
                  costUsd: Math.round(tokens * 0.0000075 * 100) / 100,
                  attempts: 4,
                  durationMs: 30 * MIN,
                },
              ],
        more: 0,
      }
    })
    return {
      days,
      since: new Date(since).toISOString(),
      until: new Date(until).toISOString(),
      previousSince: new Date(since - days * DAY).toISOString(),
      dataSince: ago(45 * DAY),
      totals: totals(days, 1),
      previous: totals(days, 0.82),
      daily,
      projects: projectRows.slice(0, 5),
      moreProjects: Math.max(0, projectRows.length - 5),
      accounts: accountRows.slice(0, 5),
      moreAccounts: Math.max(0, accountRows.length - 5),
    }
  }

  /** Remaining of a window at time `t`: falls during work hours and refills at each reset. */
  function remainingAt(t, period, burn, offset) {
    const phase = ((t + offset) % period) / period
    const hour = new Date(t).getHours()
    const work = hour >= 9 && hour < 19 ? 1 : 0.2
    return Math.max(0, Math.min(1, 1 - phase * burn * work))
  }

  function windowsAt(account, t) {
    const out = []
    account.quota.groups.forEach((g, gi) => {
      for (const w of g.windows) {
        const period = w.window === 'weekly' ? 7 * DAY : 5 * HOUR
        const offset = (gi + 1) * 37 * MIN + account.id.length * 11 * MIN
        const burn = 0.5 + ((account.priority + gi) % 5) * 0.12
        out.push({
          group: g.name,
          window: w.window,
          remaining: Math.round(remainingAt(t, period, burn, offset) * 100) / 100,
          resetAt: new Date(t + period - ((t + offset) % period)).toISOString(),
        })
      }
    })
    return out
  }

  function history(url) {
    const bucket = url.searchParams.get('bucket') ?? 'hour'
    if (!['hour', 'day'].includes(bucket)) throw invalid('bucket is hour or day', 'bucket')
    const until = Date.now()
    const rawSince = url.searchParams.get('since')
    let since = rawSince ? Date.parse(rawSince) : until - 7 * DAY
    if (Number.isNaN(since)) throw invalid('since is not a time', 'since')
    since = Math.max(since, until - 30 * DAY)
    const only = url.searchParams.get('accountId')
    const list = accounts.filter(
      (a) => (!only || a.id === only) && a.kind !== 'api-key' && a.quota.groups.length,
    )
    const start = Math.ceil(since / HOUR) * HOUR
    const base = {
      bucket,
      since: new Date(since).toISOString(),
      until: new Date(until).toISOString(),
      firstAt: ago(45 * DAY),
    }
    const snapshot = (a, t) => ({
      at: new Date(t).toISOString(),
      accountId: a.id,
      backend: a.backend,
      label: a.label,
      enabled: a.enabled,
      status: a.status,
      plan: a.quota.plan,
      quota: windowsAt(a, t),
      tokensToday: Math.round((new Date(t).getHours() / 24) * 380_000),
      costToday: 0,
    })
    if (bucket === 'hour') {
      const rows = []
      for (let t = start; t <= until; t += HOUR) for (const a of list) rows.push(snapshot(a, t))
      return { ...base, rows }
    }
    const days = new Map()
    for (let t = start; t <= until; t += HOUR) {
      for (const a of list) {
        const s = snapshot(a, t)
        const key = `${localDate(t)}|${a.id}`
        const day = days.get(key)
        if (!day) {
          days.set(key, {
            date: localDate(t),
            accountId: a.id,
            backend: a.backend,
            label: a.label,
            enabled: a.enabled,
            status: a.status,
            plan: a.quota.plan,
            samples: 1,
            firstAt: s.at,
            lastAt: s.at,
            quota: s.quota.map((w) => ({ ...w, minRemaining: w.remaining })),
            tokensToday: s.tokensToday,
            costToday: 0,
          })
          continue
        }
        day.samples++
        day.lastAt = s.at
        day.tokensToday = Math.max(day.tokensToday, s.tokensToday)
        s.quota.forEach((w, i) => {
          const d = day.quota[i]
          if (d) {
            d.remaining = w.remaining
            d.resetAt = w.resetAt
            d.minRemaining = Math.min(d.minRemaining, w.remaining)
          }
        })
      }
    }
    return { ...base, days: [...days.values()] }
  }

  // -------------------------------------------------------------------------------------------------------------
  // Settings

  const DEFAULTS = structuredClone(settings)
  const INT_RANGES = {
    maxConcurrentPerAccount: [1, 64],
    quotaRefreshMinutes: [1, 10080],
    passiveCooldownMinutes: [1, 10080],
    taskTimeoutMinutes: [1, 10080],
    stallMinutes: [1, 10080],
    stallToolMinutes: [1, 10080],
    loopRepeat: [2, 100],
    attentionDecayHours: [1, 8760],
    gateTimeoutSec: [1, 86400],
    gateMaxAutoFixes: [0, 10],
    gateSettleMinutes: [0, 10080],
    gateSettlePollMs: [100, 3600000],
    gateOutputTailBytes: [1, 1048576],
    laneHoldHours: [0, 8760],
    snapshotRetentionDays: [1, 3650],
    contentionWindowHours: [1, 8760],
    githubPollMinutes: [0, 10080],
  }
  const BOOLS = [
    'usageHistory',
    'gates',
    'gateAutoChecks',
    'hygiene',
    'verifier',
    'gitGuard',
    'codeGraph',
    'automationsEnabled',
  ]

  function merge(target, patch, defaults) {
    for (const [key, value] of Object.entries(patch)) {
      if (value === null) target[key] = structuredClone(defaults?.[key])
      else if (isObject(value) && isObject(target[key])) merge(target[key], value, defaults?.[key])
      else target[key] = value
    }
  }

  function validateSettings(next, patch) {
    const problems = []
    const bad = (field, message) => problems.push({ field, message })
    const known = (obj, shape, prefix) => {
      for (const key of Object.keys(obj)) {
        if (!(key in shape)) bad(`${prefix}${key}`, 'unknown setting')
        else if (isObject(obj[key]) && isObject(shape[key]))
          known(obj[key], shape[key], `${prefix}${key}.`)
      }
    }
    known(patch, DEFAULTS, '')
    if (next.port !== settings.port)
      bad('port', 'the port cannot change here; use seelegent config or the command line')
    for (const [key, [min, max]] of Object.entries(INT_RANGES)) {
      const v = next[key]
      if (!Number.isInteger(v) || v < min || v > max)
        bad(key, `must be a whole number from ${min} to ${max}`)
    }
    for (const key of BOOLS) if (typeof next[key] !== 'boolean') bad(key, 'must be true or false')
    if (typeof next.minRemaining !== 'number' || next.minRemaining < 0 || next.minRemaining >= 1) {
      bad('minRemaining', 'must be at least 0% and below 100%')
    }
    if (!TIERS.includes(next.defaultTier)) bad('defaultTier', `must be one of ${TIERS.join(', ')}`)
    if (!TIERS.includes(next.verifierTier))
      bad('verifierTier', `must be one of ${TIERS.join(', ')}`)
    if (!['off', 'warn', 'enforce'].includes(next.lanes))
      bad('lanes', 'must be off, warn or enforce')
    if (!Array.isArray(next.linkIntoProfiles)) bad('linkIntoProfiles', 'must be a list')
    else {
      const seen = new Set()
      for (const entry of next.linkIntoProfiles) {
        if (typeof entry !== 'string' || !entry.trim())
          bad('linkIntoProfiles', 'entries cannot be empty')
        else if (/^([a-z]:|[\\/])/i.test(entry) || entry.split(/[\\/]/).includes('..')) {
          bad('linkIntoProfiles', `"${entry}" must be relative to your home folder`)
        } else if (seen.has(entry)) bad('linkIntoProfiles', `"${entry}" is listed twice`)
        seen.add(entry)
      }
    }
    for (const id of ['agy', 'codex', 'claude']) {
      const b = next.backends?.[id]
      if (!isObject(b)) {
        bad(`backends.${id}`, 'missing')
        continue
      }
      if (typeof b.enabled !== 'boolean') bad(`backends.${id}.enabled`, 'must be true or false')
      let allowed = null
      try {
        allowed = b.allowedModels ? new RegExp(b.allowedModels) : null
      } catch {
        bad(`backends.${id}.allowedModels`, 'is not a valid regular expression')
      }
      for (const tier of TIERS) {
        const model = b.tiers?.[tier]
        if (typeof model !== 'string') bad(`backends.${id}.tiers.${tier}`, 'must be text')
        else if (/\s/.test(model)) bad(`backends.${id}.tiers.${tier}`, 'a model name has no spaces')
        else if (model && allowed && !allowed.test(model)) {
          bad(`backends.${id}.tiers.${tier}`, 'does not match the allowed models')
        }
      }
    }
    if (
      !['read-only', 'workspace-write', 'danger-full-access'].includes(
        next.backends?.codex?.sandbox,
      )
    ) {
      bad('backends.codex.sandbox', 'must be read-only, workspace-write or danger-full-access')
    }
    return problems
  }

  async function patchSettings(req, res) {
    const patch = await readBody(req, Object.keys(DEFAULTS))
    const next = structuredClone(settings)
    merge(next, patch, DEFAULTS)
    const problems = validateSettings(next, patch)
    if (problems.length) {
      throw new HttpError(400, 'invalid_argument', `${problems.length} setting(s) are not valid`, {
        fields: problems,
      })
    }
    for (const key of Object.keys(settings)) delete settings[key]
    Object.assign(settings, next)
    broadcast('settings.updated', settings)
    return send(res, 200, settings)
  }

  // -------------------------------------------------------------------------------------------------------------
  // Memory and hooks

  const memories = new Map([
    [
      'D:/Work/shop-api',
      {
        text: '# shop-api\n\n- Run `go test ./...` before reporting.\n- Money is integer cents; never floats.\n',
        updatedAt: ago(3 * DAY),
      },
    ],
  ])

  let hookCounter = 2
  let hooks = [
    {
      id: 'h1',
      on: ['task.done', 'task.failed'],
      run: 'powershell -File C:/Users/dev/notify.ps1',
      timeoutSeconds: 20,
      enabled: true,
      lastRun: {
        hookId: 'h1',
        event: 'task.done',
        at: ago(40 * MIN),
        exitCode: 0,
        durationMs: 412,
        timedOut: false,
      },
    },
    {
      id: 'h2',
      on: ['account.needs_login'],
      run: 'node scripts/ping-slack.mjs',
      project: 'D:/Work/shop-api',
      timeoutSeconds: 10,
      enabled: false,
      lastRun: null,
    },
  ]

  function checkHooks(list) {
    if (!Array.isArray(list)) throw invalid('hooks must be a list', 'hooks')
    const problems = []
    list.forEach((h, i) => {
      const at = `hooks[${i}]`
      if (!isObject(h)) return problems.push({ field: at, message: 'must be an object' })
      if (!Array.isArray(h.on) || h.on.length === 0)
        problems.push({ field: `${at}.on`, message: 'choose at least one event' })
      else if (h.on.some((e) => !HOOK_EVENTS.includes(e)))
        problems.push({ field: `${at}.on`, message: 'unknown event' })
      if (typeof h.run !== 'string' || !h.run.trim())
        problems.push({ field: `${at}.run`, message: 'the command is required' })
      if (!Number.isInteger(h.timeoutSeconds) || h.timeoutSeconds < 1 || h.timeoutSeconds > 600) {
        problems.push({
          field: `${at}.timeoutSeconds`,
          message: 'must be a whole number from 1 to 600',
        })
      }
      if (h.id && !hooks.some((old) => old.id === h.id))
        problems.push({ field: `${at}.id`, message: 'unknown hook' })
    })
    if (problems.length)
      throw new HttpError(400, 'invalid_argument', 'some hooks are not valid', { fields: problems })
  }

  // -------------------------------------------------------------------------------------------------------------
  // Roles

  const template = (id, name, description, rules, skills, defaultTier) => ({
    id,
    name,
    description,
    rules,
    outputStyle: '',
    skills,
    defaultTier,
    preferredAccounts: [],
    scope: 'global',
    overridesGlobal: false,
  })
  const templates = [
    template(
      'frontend',
      'Frontend',
      'Vue and TypeScript screens, accessible and responsive.',
      'Use the design tokens. Every text goes through i18n. Check keyboard use.',
      ['frontend-design'],
      'standard',
    ),
    template(
      'backend',
      'Backend',
      'Go services, APIs and storage.',
      'Keep handlers thin. Every blocking call takes a context. Add tests next to the code.',
      ['tdd'],
      'standard',
    ),
    template(
      'qa',
      'QA',
      'Finds bugs and writes regression tests.',
      'Reproduce first, then write the failing test, then report. Never fix silently.',
      ['tdd'],
      'strong',
    ),
    template(
      'docs',
      'Docs',
      'Writes and trims documentation.',
      'Short sentences. Show the command, then what it prints.',
      [],
      'fast',
    ),
  ]
  const userSkills = [
    {
      name: 'frontend-design',
      description: 'Distinctive, intentional visual design.',
      root: 'user',
    },
    { name: 'tdd', description: 'Red, green, refactor.', root: 'user' },
    { name: 'speckit-plan', description: 'Turn a spec into a plan.', root: 'user' },
    { name: 'code-review', description: 'Review a diff for bugs.', root: 'claude' },
    { name: 'gemini-search', description: 'Search the web from a worker.', root: 'gemini' },
  ]
  const projectSkills = new Map([
    [
      'D:/Work/shop-api',
      [
        {
          name: 'shop-conventions',
          description: 'Money in cents, errors wrapped.',
          root: 'project',
        },
      ],
    ],
  ])
  const at = ago(10 * DAY)
  let roles = [
    { ...templates[0], createdAt: at, updatedAt: at },
    { ...templates[2], skills: ['tdd', 'old-skill'], createdAt: at, updatedAt: ago(2 * DAY) },
    {
      ...templates[1],
      id: 'backend',
      scope: 'project',
      cwd: 'D:/Work/shop-api',
      skills: ['tdd', 'shop-conventions'],
      preferredAccounts: ['acc-work'],
      defaultEffort: 'high',
      createdAt: at,
      updatedAt: at,
    },
    {
      ...templates[2],
      scope: 'project',
      cwd: 'D:/Work/shop-api',
      rules: 'Also run the contract tests in ./contract before reporting.',
      overridesGlobal: true,
      createdAt: at,
      updatedAt: at,
    },
  ]

  const roleProject = (cwd) => projects.find((p) => p.cwd.toLowerCase() === cwd.toLowerCase())

  function roleList(url) {
    const cwd = url.searchParams.get('cwd')
    const all = url.searchParams.get('all') === '1'
    const withOverride = roles.map((r) => ({
      ...r,
      overridesGlobal:
        r.scope === 'project' && roles.some((g) => g.scope === 'global' && g.id === r.id),
    }))
    const items = withOverride.filter(
      (r) => r.scope === 'global' || all || (cwd && r.cwd.toLowerCase() === cwd.toLowerCase()),
    )
    // Like Go's role.Store.Projects: only folders that have project roles (the editor adds /state's projects).
    const counts = new Map()
    for (const r of roles) {
      if (r.scope !== 'project') continue
      const known = roleProject(r.cwd)
      const cwd = known?.cwd ?? r.cwd
      const row = counts.get(cwd.toLowerCase()) ?? {
        cwd,
        name: known?.name ?? cwd.split(/[\\/]/).filter(Boolean).pop() ?? cwd,
        count: 0,
      }
      row.count += 1
      counts.set(cwd.toLowerCase(), row)
    }
    const projectRows = [...counts.values()].map((p) => ({
      key: projectKey(p.cwd, p.name),
      ...p,
    }))
    return { items, projects: projectRows }
  }

  function resolveProject(value) {
    if (!value) throw invalid('cwd is required for a project role', 'cwd')
    const byKey = projects.find((p) => projectKey(p.cwd, p.name) === value)
    if (byKey) return byKey.cwd
    if (!/^([a-z]:[\\/]|\/)/i.test(value))
      throw invalid('cwd must be an absolute folder or a project key', 'cwd')
    return roleProject(value)?.cwd ?? value
  }

  function checkRole(role) {
    const problems = []
    const bad = (field, message) => problems.push({ field: `role.${field}`, message })
    const allowed = [
      'id',
      'name',
      'description',
      'rules',
      'outputStyle',
      'skills',
      'defaultTier',
      'defaultEffort',
      'preferredAccounts',
    ]
    for (const key of Object.keys(role)) if (!allowed.includes(key)) bad(key, 'unknown field')
    if (typeof role.id !== 'string' || !ID.test(role.id))
      bad('id', 'use a-z, 0-9 and -, starting with a letter or digit, at most 40')
    if (typeof role.name !== 'string' || !role.name.trim() || role.name.length > 60)
      bad('name', 'is required, at most 60 characters')
    const text = (key, max) => {
      if (role[key] !== undefined && (typeof role[key] !== 'string' || role[key].length > max))
        bad(key, `at most ${max} characters`)
    }
    text('description', 300)
    text('rules', 8000)
    text('outputStyle', 2000)
    if (role.skills !== undefined && (!Array.isArray(role.skills) || role.skills.length > 20))
      bad('skills', 'at most 20 skills')
    if (role.defaultTier !== undefined && !TIERS.includes(role.defaultTier))
      bad('defaultTier', 'unknown tier')
    if (role.defaultEffort !== undefined && !EFFORTS.includes(role.defaultEffort))
      bad('defaultEffort', 'unknown effort')
    if (role.preferredAccounts !== undefined && !Array.isArray(role.preferredAccounts))
      bad('preferredAccounts', 'must be a list')
    if (problems.length)
      throw new HttpError(400, 'invalid_argument', 'the role is not valid', { fields: problems })
  }

  async function roleRoutes(req, res, method, parts, url) {
    if (parts.length === 1) {
      if (method === 'GET') return send(res, 200, roleList(url))
      if (method !== 'POST') return false
      const body = await readBody(req, ['role', 'scope', 'cwd'])
      if (!isObject(body.role)) throw invalid('role is required', 'role')
      if (!['global', 'project'].includes(body.scope))
        throw invalid('scope is global or project', 'scope')
      checkRole(body.role)
      const cwd = body.scope === 'project' ? resolveProject(body.cwd) : undefined
      const old = roles.find(
        (r) =>
          r.id === body.role.id &&
          r.scope === body.scope &&
          (body.scope === 'global' || r.cwd === cwd),
      )
      const saved = {
        description: '',
        rules: '',
        outputStyle: '',
        skills: [],
        preferredAccounts: [],
        ...body.role,
        name: body.role.name.trim(),
        scope: body.scope,
        ...(cwd ? { cwd } : {}),
        overridesGlobal: false,
        createdAt: old?.createdAt ?? now(),
        updatedAt: now(),
      }
      roles = [...roles.filter((r) => r !== old), saved]
      broadcast('roles.changed', { changedAt: now() })
      return send(res, old ? 200 : 201, saved)
    }
    if (parts[1] === 'templates' && method === 'GET') return send(res, 200, templates)
    if (parts[1] === 'skills' && method === 'GET') {
      const cwd = url.searchParams.get('cwd')
      return send(res, 200, [
        ...(cwd ? (projectSkills.get(roleProject(cwd)?.cwd ?? cwd) ?? []) : []),
        ...userSkills,
      ])
    }
    if (method === 'DELETE') {
      const scope = url.searchParams.get('scope')
      if (!['global', 'project'].includes(scope))
        throw invalid('scope is global or project', 'scope')
      const cwd = scope === 'project' ? resolveProject(url.searchParams.get('cwd')) : undefined
      const found = roles.find(
        (r) => r.id === parts[1] && r.scope === scope && (scope === 'global' || r.cwd === cwd),
      )
      if (!found) throw notFound(`role ${parts[1]}`)
      roles = roles.filter((r) => r !== found)
      broadcast('roles.changed', { changedAt: now() })
      return send(res, 204)
    }
    return false
  }

  // -------------------------------------------------------------------------------------------------------------
  // Router: returns false when the route is not one of these.

  return async function adminRoute(req, res, method, path, parts, url) {
    if (parts[0] === 'accounts') return accountRoutes(req, res, method, parts)
    if (path === '/quota/refresh' && method === 'POST') {
      await readBody(req, [])
      return send(
        res,
        200,
        ordered()
          .filter((a) => a.enabled)
          .map(refresh),
      )
    }
    if (parts[0] === 'providers') return providerRoutes(req, res, method, parts)
    if (path === '/usage/summary' && method === 'GET') {
      const days = Number(url.searchParams.get('days') ?? 7)
      if (!Number.isInteger(days) || days < 1 || days > 30) throw invalid('days is 1 to 30', 'days')
      return send(res, 200, summary(days))
    }
    if (path === '/usage/history' && method === 'GET') return send(res, 200, history(url))
    if (path === '/settings') {
      if (method === 'GET') return send(res, 200, settings)
      if (method === 'PATCH') return patchSettings(req, res)
    }
    if (path === '/memory') {
      if (method === 'GET') {
        const cwd = url.searchParams.get('cwd')
        if (!cwd) throw invalid('cwd is required', 'cwd')
        const found = memories.get(cwd)
        return send(res, 200, {
          cwd,
          text: found?.text ?? '',
          ...(found ? { updatedAt: found.updatedAt } : {}),
        })
      }
      if (method === 'PUT') {
        const body = await readBody(req, ['cwd', 'text'])
        if (typeof body.cwd !== 'string' || !body.cwd) throw invalid('cwd is required', 'cwd')
        if (typeof body.text !== 'string') throw invalid('text is required', 'text')
        if (body.text.length > 65536) throw invalid('memory is limited to 64 KiB', 'text')
        const saved = { text: normaliseMemory(body.text), updatedAt: now() }
        memories.set(body.cwd, saved)
        broadcast('memory.changed', { cwd: body.cwd })
        return send(res, 200, { cwd: body.cwd, ...saved })
      }
    }
    if (parts[0] === 'hooks') {
      if (noHooks) {
        throw new HttpError(
          501,
          'not_implemented',
          'hooks are not available in this version of the daemon',
        )
      }
      if (parts.length === 1 && method === 'GET') return send(res, 200, hooks)
      if (parts.length === 1 && method === 'PUT') {
        const body = await readBody(req, ['hooks'])
        checkHooks(body.hooks)
        hooks = body.hooks.map((h) => {
          const id = h.id || `h${++hookCounter}`
          const old = hooks.find((o) => o.id === id)
          return {
            id,
            on: h.on,
            run: h.run.trim(),
            ...(h.project ? { project: h.project } : {}),
            timeoutSeconds: h.timeoutSeconds,
            enabled: h.enabled !== false,
            lastRun: old?.lastRun ?? null,
          }
        })
        return send(res, 200, hooks)
      }
      if (parts.length === 3 && parts[2] === 'test' && method === 'POST') {
        await readBody(req, [])
        if (!hooks.some((h) => h.id === parts[1])) throw notFound(`hook ${parts[1]}`)
        throw new HttpError(
          501,
          'not_implemented',
          'testing hooks is not available in this version of the daemon',
        )
      }
    }
    if (parts[0] === 'roles') return roleRoutes(req, res, method, parts, url)
    return false
  }
}
