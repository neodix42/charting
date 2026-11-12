import {test,expect,type Page} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import type {TradeRecord} from '../src/workspace/tradeHistory';

test.use({actionTimeout:10000});

const outer=(page:Page)=>page.getByRole('tabpanel',{name:'Trade history',exact:true});
const gallery=(page:Page)=>outer(page).getByRole('tabpanel',{name:'Filtering',exact:true});
const viewer=(page:Page)=>page.getByRole('dialog',{name:'Trade screen',exact:true});
const cards=(page:Page)=>gallery(page).locator('.trade-filter-card');
const tradeId=(i:number,provider:'dukascopy'|'hyperliquid'='dukascopy')=>`${provider}:${i.toString(16).padStart(32,'0')}`;
function record(i:number,provider:'dukascopy'|'hyperliquid',condition:string,direction='long',profitLoss='12',status='closed'):TradeRecord{
 return{id:tradeId(i,provider),provider,environment:provider==='dukascopy'?'demo':'testnet',account:'offline-gallery-account',instrument:`ASSET${i}`,market:provider==='dukascopy'?'forex':'perpetual',orderId:String(i),submittedAt:Date.UTC(2026,10,11),openDate:Date.UTC(2026,10,11),closeDate:status==='closed'?Date.UTC(2026,10,11,1):0,size:'1',remainingSize:status==='closed'?'0':'1',direction,entryPrice:'100',stopLoss:'90',takeProfit:'120',slAmount:'-10',tpAmount:'20',currency:'USD',balanceAtOpen:'1000',balanceCurrency:'USD',balanceTime:Date.UTC(2026,10,11),marginUsed:'',marginCurrency:'',status,canClose:false,closeReason:'',checkedAt:Date.now(),entryCondition:condition,profitLoss,profitLossState:status==='closed'?'closed':'open',profitLossCurrency:'USD',profitLossTime:Date.now()};
}
async function enter(page:Page){await page.getByRole('tab',{name:'Trade history',exact:true}).click();await expect.poll(()=>page.evaluate(()=>(window as any).__testTradeHistoryReads())).toBeGreaterThan(0);await expect(outer(page).getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();}
async function filtering(page:Page){await outer(page).getByRole('tab',{name:'Filtering',exact:true}).click();await expect(gallery(page)).toBeVisible();await expect(gallery(page).getByRole('status')).not.toHaveText('Loading trade images…');}
async function setup(page:Page){
 await openTestApp(page);
 const rows=[record(1,'dukascopy','Condition X'),record(2,'hyperliquid','Condition X','short','-3'),record(3,'hyperliquid','Condition Y'),record(4,'dukascopy','Condition Y','short','-5'),record(5,'dukascopy','Condition X','long','20','open'),record(6,'hyperliquid','Condition X','long','0'),record(7,'dukascopy','Condition X','long',''),record(8,'hyperliquid','Condition XY','long','-2'),...Array.from({length:23},(_,i)=>record(i+9,'dukascopy','')),record(32,'dukascopy','Later condition'),record(33,'hyperliquid','No image condition')];
 await page.evaluate(items=>(window as any).__testSetTradeHistory(items),rows);
 const original=await page.evaluate(()=>{const canvas=document.createElement('canvas');canvas.width=120;canvas.height=80;const context=canvas.getContext('2d')!;context.fillStyle='#2e7ea6';context.fillRect(0,0,120,80);return canvas.toDataURL('image/png');});
 await page.evaluate(async({rows,original})=>{const api=window.go!.main!.App!;for(const row of rows.filter(row=>Number(row.orderId)<=8||row.orderId==='32'))await api.AddTradeScreen(row.id,`screen-${row.orderId}.png`,original);await api.AddTradeScreen(rows[0].id,'second-1.png',original);(window as any).__galleryOriginal=original;}, {rows,original});
 await enter(page);
 await page.evaluate(()=>{const api=window.go!.main!.App!,get=api.GetTradeScreen,getGallery=api.GetTradeScreenGallery!;(window as any).__galleryReads=[];(window as any).__galleryQueries=[];api.GetTradeScreen=async(trade,id)=>{(window as any).__galleryReads.push({trade,id});return get(trade,id);};api.GetTradeScreenGallery=async query=>{(window as any).__galleryQueries.push(structuredClone(query));return getGallery(query);};});
}
async function choose(page:Page,name:string){await gallery(page).getByRole('button',{name,exact:true}).click();await expect(gallery(page).getByRole('status')).not.toHaveText('Loading trade images…');}
async function expectCount(page:Page,n:number){await expect(gallery(page).getByRole('status')).toHaveText(`${n} ${n===1?'image':'images'}`);await expect(cards(page)).toHaveCount(n);}

test('Filtering facets cover all history and combined pills use exact conditions, direction, outcome and exchange',async({page})=>{
 await setup(page);const historyReads=await page.evaluate(()=>(window as any).__testTradeHistoryReads()),refreshes=await page.evaluate(()=>(window as any).__testTradeHistoryRefreshes().length);
 await filtering(page);await expect(cards(page)).toHaveCount(0);await expect(gallery(page).getByRole('status')).toContainText('Choose one or more filters');
 await expect(gallery(page).getByRole('button',{name:'Later condition',exact:true})).toBeVisible();await expect(gallery(page).getByRole('button',{name:'No image condition',exact:true})).toBeVisible();expect(await page.evaluate(()=>(window as any).__galleryReads.length)).toBe(0);
 await choose(page,'Condition X');await expectCount(page,6);await choose(page,'Long');await expectCount(page,5);await choose(page,'Won');await expectCount(page,2);await choose(page,'Dukascopy');await expectCount(page,2);
 await expect(gallery(page).locator('.trade-filter-caption')).toContainText(['ASSET1','ASSET1']);await expect(gallery(page).locator('.trade-filter-caption').first()).toContainText('Won · 12.00 USD');
 await expect(gallery(page).locator('.trade-filter-preview').first().getByRole('img')).toBeVisible();await page.screenshot({path:'../.tools/verification/trade-gallery-ui.png',fullPage:true});
 await choose(page,'Hyperliquid');await expectCount(page,2);await choose(page,'Dukascopy');await expectCount(page,0);await choose(page,'Won');await expectCount(page,1);await expect(gallery(page).locator('.trade-filter-caption')).toContainText('ASSET6');
 await page.clock.install();await page.clock.fastForward(25000);expect(await page.evaluate(()=>(window as any).__testTradeHistoryReads())).toBe(historyReads);expect(await page.evaluate(()=>(window as any).__testTradeHistoryRefreshes().length)).toBe(refreshes);
 await gallery(page).getByRole('button',{name:'Clear all',exact:true}).click();await expect(cards(page)).toHaveCount(0);await expect(gallery(page).locator('button[aria-pressed=true]')).toHaveCount(0);await expect(gallery(page).getByRole('status')).toContainText('Choose one or more filters');
});

test('multiple choices within a group include either value and Clear all defeats stale in-flight results',async({page})=>{
 await setup(page);await filtering(page);await choose(page,'Condition X');await choose(page,'Condition XY');await expectCount(page,7);await choose(page,'Long');await expectCount(page,6);await choose(page,'Short');await expectCount(page,7);await choose(page,'Won');await expectCount(page,2);await choose(page,'Lost');await expectCount(page,4);await choose(page,'Hyperliquid');await expectCount(page,2);await choose(page,'Dukascopy');await expectCount(page,4);
 await page.evaluate(()=>{const api=window.go!.main!.App!,get=api.GetTradeScreenGallery!;api.GetTradeScreenGallery=async query=>{const result=await get(query);if(query.conditions?.includes('Condition Y'))return new Promise(resolve=>(window as any).__releaseOldGallery=()=>resolve(result));return result;};});
 await gallery(page).getByRole('button',{name:'Condition Y',exact:true}).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseOldGallery)).toBe('function');await gallery(page).getByRole('button',{name:'Clear all',exact:true}).click();await page.evaluate(()=>(window as any).__releaseOldGallery());await expect(cards(page)).toHaveCount(0);await expect(gallery(page).locator('button[aria-pressed=true]')).toHaveCount(0);await expect(gallery(page).getByRole('status')).toContainText('Choose one or more filters');
});

test('gallery deletion persists across the History table and reload without erasing other attachments',async({page})=>{
 await setup(page);await filtering(page);await choose(page,'Condition X');await choose(page,'Long');await choose(page,'Won');await expectCount(page,2);await gallery(page).locator('.trade-filter-preview').first().click();await expect(viewer(page).getByRole('img')).toBeVisible();
 await viewer(page).getByRole('button',{name:'Remove image',exact:true}).click();await expect(viewer(page)).toHaveCount(0);await expectCount(page,1);
 await outer(page).getByRole('tab',{name:'History',exact:true}).click();const first=outer(page).getByRole('row').filter({has:page.getByRole('cell',{name:'ASSET1',exact:true})});await expect(first.getByRole('button',{name:/Open screen/})).toHaveCount(1);await expect(outer(page).getByRole('row').filter({hasText:'ASSET2'}).getByRole('button',{name:/Open screen/})).toHaveCount(1);
 await reloadTestApp(page);await enter(page);await filtering(page);await choose(page,'Condition X');await choose(page,'Long');await choose(page,'Won');await expectCount(page,1);await expect(gallery(page).locator('.trade-filter-caption')).toContainText('ASSET1');
});

test('pagination loads every matching image beyond the history page and notes refresh condition facets',async({page})=>{
 await setup(page);await page.evaluate(async()=>{const api=window.go!.main!.App!,id='dukascopy:'+(32).toString(16).padStart(32,'0'),original=(window as any).__galleryOriginal;for(let i=0;i<26;i++)await api.AddTradeScreen(id,`later-${i}.png`,original);});await filtering(page);await choose(page,'Later condition');await expect(cards(page)).toHaveCount(24);await expect(gallery(page).getByRole('status')).toHaveText('27 images');await gallery(page).getByRole('button',{name:'Next images',exact:true}).click();await expect(cards(page)).toHaveCount(3);await expect(gallery(page).getByRole('navigation',{name:'Trade image pages'})).toContainText('25–27 of 27 images');
 await outer(page).getByRole('tab',{name:'History',exact:true}).click();const first=outer(page).getByRole('row').filter({has:page.getByRole('cell',{name:'ASSET1',exact:true})});await first.getByRole('button',{name:/edit entry condition/i}).click();await first.getByRole('textbox').fill('Renamed condition');await first.getByRole('textbox').press('Enter');await filtering(page);await expect(gallery(page).getByRole('button',{name:'Renamed condition',exact:true})).toBeVisible();
 await gallery(page).getByRole('button',{name:'Clear all',exact:true}).click();await choose(page,'Renamed condition');await expectCount(page,2);await expect(gallery(page).locator('.trade-filter-preview').first().getByRole('img')).toBeVisible();await page.screenshot({path:'../.tools/verification/trade-gallery-renamed-ui.png',fullPage:true});
});

test('a committed removal warning keeps the image removed while preserving cleanup details',async({page})=>{
 await setup(page);await page.evaluate(()=>{const api=window.go!.main!.App!,remove=api.RemoveTradeScreen!;api.RemoveTradeScreen=async(trade,id)=>({...await remove(trade,id),warning:'The attachment was removed, but its original file could not be deleted.'});});
 await filtering(page);await choose(page,'Later condition');await expectCount(page,1);await gallery(page).locator('.trade-filter-preview').click();await expect(viewer(page).getByRole('img')).toBeVisible();await viewer(page).getByRole('button',{name:'Remove image',exact:true}).click();await expect(viewer(page)).toHaveCount(0);await expectCount(page,0);await expect(gallery(page).getByRole('alert')).toContainText('original file could not be deleted');
 await gallery(page).getByRole('button',{name:'Clear all',exact:true}).click();await expect(cards(page)).toHaveCount(0);await choose(page,'Condition Y');await expectCount(page,2);
});

test('a History deletion cannot redisplay a stale gallery card while its new query is pending',async({page})=>{
 await setup(page);await filtering(page);await choose(page,'Condition X');await choose(page,'Long');await choose(page,'Won');await expectCount(page,2);
 await outer(page).getByRole('tab',{name:'History',exact:true}).click();const first=outer(page).getByRole('row').filter({has:page.getByRole('cell',{name:'ASSET1',exact:true})});await first.getByRole('button',{name:/Open screen/}).first().click();await expect(viewer(page).getByRole('img')).toBeVisible();await viewer(page).getByRole('button',{name:'Remove image',exact:true}).click();await expect(first.getByRole('button',{name:/Open screen/})).toHaveCount(1);await viewer(page).getByRole('button',{name:'Close screens window',exact:true}).click();await expect(viewer(page)).toHaveCount(0);
 await page.evaluate(()=>{const api=window.go!.main!.App!,get=api.GetTradeScreenGallery!;api.GetTradeScreenGallery=async query=>{const result=await get(query);return new Promise(resolve=>(window as any).__releaseRevisedGallery=()=>resolve(result));};});await outer(page).getByRole('tab',{name:'Filtering',exact:true}).click();await expect.poll(()=>page.evaluate(()=>typeof(window as any).__releaseRevisedGallery)).toBe('function');await expect(cards(page)).toHaveCount(0);await expect(gallery(page).getByRole('status')).toHaveText('Loading trade images…');await page.evaluate(()=>(window as any).__releaseRevisedGallery());await expectCount(page,1);
});

test('many condition chips leave Clear all and image cards reachable',async({page})=>{
 await setup(page);const extra=Array.from({length:120},(_,i)=>record(i+100,'dukascopy',`Facet condition ${i+1}`));await page.evaluate(items=>(window as any).__testSetTradeHistory([...(window as any).__testTradeHistory(),...items]),extra);await filtering(page);await choose(page,'Condition X');await expectCount(page,6);
 const toolbar=gallery(page).locator('.trade-filter-toolbar');expect((await toolbar.boundingBox())!.height).toBeLessThanOrEqual(321);await toolbar.evaluate(element=>{element.scrollTop=element.scrollHeight;});await expect(gallery(page).getByRole('button',{name:'Clear all',exact:true})).toBeInViewport();await expect(cards(page).first()).toBeInViewport({ratio:.1});await gallery(page).getByRole('button',{name:'Clear all',exact:true}).click();await expect(cards(page)).toHaveCount(0);
});
