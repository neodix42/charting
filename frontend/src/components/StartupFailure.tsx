/** A module failure must stay actionable even when React could not be fetched. */
export function showStartupFailure(host:HTMLElement){
 const screen=document.createElement('main');screen.className='boot-screen';
 const card=document.createElement('section');card.className='boot-card';card.setAttribute('aria-labelledby','boot-title');
 const title=document.createElement('h1');title.id='boot-title';title.textContent='CryptoDesk';
 const error=document.createElement('div');error.className='boot-error';error.setAttribute('role','alert');
 const heading=document.createElement('strong');heading.textContent='Unable to load the application';
 const message=document.createElement('p');message.textContent='Application files could not be loaded. Retry to reopen your saved workspace.';
 error.append(heading,message);
 const retry=document.createElement('button');retry.className='boot-retry';retry.type='button';retry.textContent='Retry loading';retry.addEventListener('click',()=>window.location.reload());
 card.append(title,error,retry);screen.append(card);host.replaceChildren(screen);
}
