/** Decimal tick precision, including increments such as 0.25 and 1e-8. */
export function tickPrecision(value:string){const [mantissa,exponent]=value.toLowerCase().split('e');const decimals=(mantissa.split('.')[1]||'').replace(/0+$/,'').length;return Math.max(0,Math.min(12,decimals-(Number(exponent)||0)));}
