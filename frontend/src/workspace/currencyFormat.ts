// Monetary display precision is separate from instrument ticks and order size.
// Keep precious metals and crypto tokens out of the fiat set.
const fiatCurrencies=new Set('AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BHD BIF BMD BND BOB BRL BSD BTN BWP BYN BZD CAD CDF CHF CLP CNH CNY COP CRC CUP CVE CZK DJF DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GNF GTQ GYD HKD HNL HTG HUF IDR ILS INR IQD IRR ISK JMD JOD JPY KES KGS KHR KMF KPW KRW KWD KYD KZT LAK LBP LKR LRD LSL LYD MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MYR MZN NAD NGN NIO NOK NPR NZD OMR PAB PEN PGK PHP PKR PLN PYG QAR RON RSD RUB RWF SAR SBD SCR SDG SEK SGD SHP SLE SOS SRD SSP STN SVC SYP SZL THB TJS TMT TND TOP TRY TTD TWD TZS UAH UGX USD UYU UZS VES VND VUV WST XAF XCD XOF XPF YER ZAR ZMW ZWG'.split(' '));

export function currencyDecimalPlaces(currency:string|undefined):number|null {
 const code=currency?.trim().toUpperCase();
 return code==='USDT'||code==='USDC'?2:code&&fiatCurrencies.has(code)?4:null;
}

interface DisplayOptions {signed?:boolean}
const nativeAmount=new Intl.NumberFormat('en-US',{minimumFractionDigits:2,maximumFractionDigits:8});
const smallNativeAmount=new Intl.NumberFormat('en-US',{maximumSignificantDigits:8});

/** Round decimal strings before grouping, preserving digits beyond Number's precision. */
function fixedDecimal(raw:string,places:number,signed:boolean):string|null {
 const parts=/^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(raw);
 if(!parts||!(parts[2]||parts[3]))return null;
 const exponent=Number(parts[4]??0),fraction=parts[3]??'',shift=places+exponent-fraction.length;
 if(!Number.isSafeInteger(exponent)||Math.abs(exponent)>4096||raw.length>8192)return null;
 let units=BigInt((parts[2]||'0')+fraction);
 if(shift>=0)units*=10n**BigInt(shift);
 else {
  const divisor=10n**BigInt(-shift),remainder=units%divisor;
  units=units/divisor+(remainder*2n>=divisor?1n:0n);
 }
 const digits=units.toString().padStart(places+1,'0'),whole=digits.slice(0,-places).replace(/\B(?=(\d{3})+(?!\d))/g,','),decimal=digits.slice(-places);
 const sign=parts[1]==='-'?'-':signed?'+':'';
 return `${sign}${whole}.${decimal}`;
}

export function formatCurrencyValue(value:string|number|undefined,currency:string|undefined,options:DisplayOptions={}):string|null {
 if(value===undefined||!currency?.trim())return null;
 const raw=String(value).trim(),number=Number(raw);
 if(!raw||!Number.isFinite(number))return null;
 const places=currencyDecimalPlaces(currency);
 if(places!==null)return fixedDecimal(raw,places,!!options.signed);
 const formatted=number!==0&&Math.abs(number)<.01?smallNativeAmount.format(number):nativeAmount.format(number);
 return options.signed&&number>=0?`+${formatted}`:formatted;
}

export function formatCurrencyAmount(value:string|number|undefined,currency:string|undefined,options:DisplayOptions={}):string|null {
 const amount=formatCurrencyValue(value,currency,options);
 return amount===null?null:`${amount} ${currency!.trim()}`;
}
