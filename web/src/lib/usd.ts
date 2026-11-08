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
