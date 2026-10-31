/** Match the local Telegram settings contract; no lookup or delivery occurs here. */
export function telegramRecipients(value:string):string[]|null{
 if(new TextEncoder().encode(value).length>4096)return null;
 const trim=(text:string)=>text.replace(/^\p{White_Space}+|\p{White_Space}+$/gu,'');
 if(!trim(value))return [];
 const recipients:string[]=[],seen=new Set<string>();
 for(const item of value.split(',')){
  let recipient=trim(item);
  if(/^@[A-Za-z0-9_]{1,32}$/.test(recipient)){
   // Usernames are case insensitive; retain the spelling first entered.
  }else if(/^\+?[0-9]+$/.test(recipient)){
   const id=BigInt(recipient);
   if(id<=0n||id>=2n**52n)return null;
   recipient=id.toString();
  }else return null;
  const identity=recipient.toLowerCase();
  if(!seen.has(identity)){seen.add(identity);recipients.push(recipient);}
  if(recipients.length>20)return null;
 }
 return recipients;
}
