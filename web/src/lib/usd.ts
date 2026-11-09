/**
 * An amount of US dollars written the way the daemon writes it (account.FormatUSD), so a budget reads the same in
 * the dashboard and in the lead's messages: whole dollars without cents ("$200"), small amounts with the digits
 * they need ("$0.155"), others with cents ("$12.40").
 */
export function formatUSD(value: number): string {
  if (Number.isInteger(value)) return `$${value}`
  if (value < 1) {
    const three = value.toFixed(3).replace(/0+$/, '')
    const decimals = three.length - three.indexOf('.') - 1
    return `$${decimals < 2 ? value.toFixed(2) : three}`
  }
  return `$${value.toFixed(2)}`
}

/**
 * An amount in the currency a service named ("USD", "CNY"), never converted: US dollars as formatUSD writes them,
 * others with the currency's narrow symbol ("¥187.60"), or the code after the number when Intl does not know it.
 */
export function formatMoney(value: number, currency: string, locale = 'en'): string {
  const code = currency.trim().toUpperCase()
  if (code === 'USD') return formatUSD(value)
  try {
    return new Intl.NumberFormat(locale, {
      style: 'currency',
      currency: code,
      currencyDisplay: 'narrowSymbol',
    }).format(value)
  } catch {
    return `${value.toFixed(2)} ${currency}`
  }
}
