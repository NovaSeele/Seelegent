/**
 * What an API-key account has spent, as the Accounts page shows it in place of quota (API keys have none). Where
 * the number comes from is the provider's spend source (contract "Spend and budget"):
 *   cli   Claude Code's own estimate (official Anthropic only): an amount with "≈", against the budget if one is set;
 *   free  Ollama or a localhost service: "Local · free";
 *   none  the service reports no cost: the tokens only. An unknown source (later: service, prices) reads as none.
 */
import type { Account } from '@/api/types'
import type { Tone } from '@/components/ui/tone'

export interface SpendView {
  kind: 'cost' | 'tokens' | 'free'
  spent: number
  tokens: number
  since?: string
  /** The budget in USD, when one is set. */
  budget?: number
  /** spent / budget (0..∞), for a cost against a budget. */
  ratio?: number
  /** The bar's colour: attention from 80% of the budget, danger at 100%. */
  tone: Tone
  /** A budget is set but the service reports no cost: it is never enforced. */
  budgetUnknown: boolean
}

export const BUDGET_WARN = 0.8

export function spendView(account: Account): SpendView | null {
  if (account.kind !== 'api-key') return null
  const source = account.spendSource
  const kind = source === 'cli' ? 'cost' : source === 'free' ? 'free' : 'tokens'
  const spent = account.spentUsd ?? 0
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
  return {
    kind,
    spent,
    tokens: account.spentTokens ?? 0,
    ...(account.spentSince ? { since: account.spentSince } : {}),
    ...(budget !== undefined ? { budget } : {}),
    ...(ratio !== undefined ? { ratio } : {}),
    tone,
    budgetUnknown: budget !== undefined && kind !== 'cost',
  }
}
