/**
 * The mock daemon's provider allow-list, kept apart so it can be tested (allow-list.spec.mjs). It follows
 * internal/httpapi applyProvider and internal/account (ModelsPattern, ModelsOf, cleanModels):
 * - `models` is the allow-list exactly as last set, never the tier models;
 * - `allowedModels` is the pattern enforced for it (exactPattern of the list); a hand-written pattern that is
 *   not such a list comes with `models: []`;
 * - both empty means any model, and tier models are always allowed on top.
 */

/** Like the daemon's ModelsPattern: an anchored pattern of exactly these models ('' for none). */
export function exactPattern(models) {
  if (!models.length) return ''
  return `^(?:${models.map((m) => m.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})$`
}

/** The models an exact pattern names, or null for a hand-written pattern. */
export function listOf(pattern) {
  const exact = /^\^\(\?:(.+)\)\$$/.exec(pattern)
  if (!exact) return null
  const parts = exact[1].split(/(?<!\\)\|/)
  if (parts.some((part) => /(^|[^\\])[.*+?^${}()|[\]]/.test(part))) return null
  return parts.map((part) => part.replace(/\\(.)/g, '$1'))
}

const invalid = (message) => Object.assign(new Error(message), { field: 'models' })

/** cleanModels: trimmed ids, blanks and repeats dropped, in order; an id with a space is refused. */
export function cleanModels(models) {
  if (!Array.isArray(models) || models.some((m) => typeof m !== 'string')) {
    throw invalid('models must be a list of model ids')
  }
  const out = []
  for (const raw of models) {
    const m = raw.trim()
    if (!m || out.includes(m)) continue
    if ([...m].some((ch) => ch.codePointAt(0) <= 32)) {
      throw invalid(`model id "${m}" must not contain spaces`)
    }
    out.push(m)
  }
  return out
}

/**
 * Applies `models` and `allowedModels` of a ProviderInput to p, in the daemon's order:
 * - a `models` with at least one id replaces the list, and `allowedModels` becomes its pattern (a given
 *   `allowedModels` is ignored);
 * - otherwise `allowedModels` sets the pattern, and `models` becomes the list it was built from, or [] for a
 *   hand-written pattern. Sent with `models: []` and equal to the pattern of the stored list, it is that list's
 *   echo and the list is cleared instead;
 * - otherwise `models: []` clears the list and the pattern: any model.
 * Both undefined leave the allow-list alone. Throws an Error with `field` for a body the daemon refuses.
 */
export function applyAllowList(p, models, allowedModels) {
  const list = models === undefined ? undefined : cleanModels(models)
  const listEcho =
    list !== undefined &&
    allowedModels !== undefined &&
    p.models.length > 0 &&
    allowedModels === p.allowedModels
  if (list?.length) {
    p.models = list
    p.allowedModels = exactPattern(list)
  } else if (allowedModels !== undefined && !listEcho) {
    if (typeof allowedModels !== 'string') {
      throw Object.assign(new Error('allowedModels must be text'), { field: 'allowedModels' })
    }
    const pattern = allowedModels.trim()
    try {
      new RegExp(pattern)
    } catch {
      throw Object.assign(new Error('allowedModels is not a valid regular expression'), {
        field: 'allowedModels',
      })
    }
    p.allowedModels = pattern
    p.models = listOf(pattern) ?? []
  } else if (list !== undefined) {
    p.models = []
    p.allowedModels = ''
  }
}
