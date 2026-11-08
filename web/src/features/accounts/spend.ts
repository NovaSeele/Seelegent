/**
 * What an API-key account has spent, as the Accounts page shows it in place of quota (API keys have none). Where
 * the number comes from is the account's spend source (contract "Spend sources"):
 *   service  the service's own count for this key (OpenRouter): its whole life, budget judged against it;
 *   prices   the attempts' tokens at the user's prices for the service;
 *   cli      Claude Code's own estimate (official Anthropic without prices): shown with "≈";
 *   free     Ollama or a localhost service: "Local · free";
 *   none     the tokens only, with "Set prices to see cost". An unknown source reads as none.
 */
import type { Account } from '@/api/types'
import type { Tone } from '@/components/ui/tone'

export type SpendSource = 'service' | 'prices' | 'cli' | 'free' | 'none'

export interface SpendView {
  source: SpendSource
  kind: 'cost' | 'tokens' | 'free'
  /** The amount is the CLI's estimate (cli): shown with "≈". */
  estimate: boolean
  spent: number
  /** Tokens to show: for `tokens`, those counted without a cost; for `prices`, those of models without a price. */
  tokens: number
  since?: string
  /** The budget in USD, when one is set. */
  budget?: number
  /** spent / budget (0..∞), for a cost against a budget. */
  ratio?: number
  /** The bar's colour: attention from 80% of the budget, danger at 100%. */
  tone: Tone
  /** A budget is set but the spend has no cost to judge it by: it is never enforced. */
  budgetUnknown: boolean
  /** The spend is tokens only until the service has prices: offer the Prices form. */
  needsPrices: boolean
  /** The service's own limit for the key (OpenRouter), offered as the budget while none is set. */
  suggested?: number
}

export const BUDGET_WARN = 0.8

const SOURCES: ReadonlySet<string> = new Set(['service', 'prices', 'cli', 'free', 'none'])

export function spendSource(account: Account): SpendSource {
  const s = account.spendSource
  return s && SOURCES.has(s) ? (s as SpendSource) : 'none'
}

export function spendView(account: Account): SpendView | null {
  if (account.kind !== 'api-key') return null
  const source = spendSource(account)
  const kind = source === 'free' ? 'free' : source === 'none' ? 'tokens' : 'cost'
  const spent = account.spentUsd ?? 0
  const unpriced = account.spentUnpricedTokens
  const tokens =
    kind === 'tokens'
      ? (unpriced ?? account.spentTokens ?? 0)
      : kind === 'cost'
        ? (unpriced ?? 0)
        : 0
  const budget = account.budgetUsd && account.budgetUsd > 0 ? account.budgetUsd : undefined
  const ratio = kind === 'cost' && budget ? spent / budget : undefined
  const state = account.budgetState
  const tone: Tone =
    ratio === undefined
      ? 'neutral'
      : state === 'reached' || ratio >= 1
        ? 'danger'
        : state === 'warn' || ratio >= BUDGET_WARN
          ? 'attention'
          : 'neutral'
  const suggested =
    budget === undefined && account.suggestedBudgetUsd && account.suggestedBudgetUsd > 0
      ? account.suggestedBudgetUsd
      : undefined
  return {
    source,
    kind,
    estimate: source === 'cli',
    spent,
    tokens,
    ...(account.spentSince ? { since: account.spentSince } : {}),
    ...(budget !== undefined ? { budget } : {}),
    ...(ratio !== undefined ? { ratio } : {}),
    tone,
    budgetUnknown: budget !== undefined && kind !== 'cost',
    needsPrices: source === 'none',
    ...(suggested !== undefined ? { suggested } : {}),
  }
}
