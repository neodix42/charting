import type {ChartConfig, Grid, Tab, Workspace} from '../types';
export const MAX_TABS=20, MAX_CHARTS=9;
export const presets = [[1,1],[1,2],[2,1],[2,2],[2,3],[3,2],[3,3]] as const;
export const uuid=()=>crypto.randomUUID();
export const equalTracks=(n:number)=>Array.from({length:n},()=>1/n);
export const grid=(rows=1,columns=1):Grid=>({rows,columns,rowFractions:equalTracks(rows),columnFractions:equalTracks(columns)});
export const liveView=()=>({mode:'live' as const,fromSec:null,toSec:null,barSpacing:6,rightOffset:5});
export const newChart=(source?:ChartConfig):ChartConfig=>source?{...structuredClone(source),id:uuid(),view:liveView()}:{id:uuid(),exchange:'binance',market:'spot',instrument:'BTCUSDT',timeframe:'1h',chartType:'candlestick',showVolume:true,priceScale:{mode:'linear',autoScale:true,manualRange:null},view:liveView()};
export const newTab=(name:string):Tab=>({id:uuid(),name,grid:grid(),charts:[newChart()]});
// Called only by explicit fixture mode or a confirmed reset. Production startup belongs to Go.
export function firstWorkspace():Workspace {const tab=newTab('Tab 1');return {schemaVersion:1,app:'cryptodesk',id:uuid(),theme:'dark',displayTimezone:'UTC',activeTabId:tab.id,window:{width:1600,height:1000,maximized:false},tabs:[tab]};}
export type Action = {type:'addTab'}|{type:'duplicateTab';id:string}|{type:'selectTab';id:string}|{type:'renameTab';id:string;name:string}|{type:'removeTab';id:string}|{type:'reorderTab';from:string;to:string}|{type:'grid';rows:number;columns:number}|{type:'tracks';axis:'rowFractions'|'columnFractions';values:number[]}|{type:'addChart';sourceId?:string}|{type:'removeChart';id:string}|{type:'reorderChart';from:string;to:string}|{type:'chart';id:string;patch:Partial<ChartConfig>}|{type:'settings';theme?:Workspace['theme'];displayTimezone?:string};
function reorder<T extends {id:string}>(items:T[],from:string,to:string) {const a=items.findIndex(x=>x.id===from),b=items.findIndex(x=>x.id===to);if(a<0||b<0)return items;const result=[...items];result.splice(b,0,result.splice(a,1)[0]);return result;}
function generatedName(w:Workspace){let n=1;while(w.tabs.some(t=>t.name===`Tab ${n}`))n++;return `Tab ${n}`;}
export function reduceWorkspace(w:Workspace,a:Action):Workspace {
 const current=w.tabs.find(t=>t.id===w.activeTabId)!;
 const change=(tab:Tab)=>({...w,tabs:w.tabs.map(t=>t.id===tab.id?tab:t)});
 switch(a.type){
 case 'addTab':{if(w.tabs.length>=MAX_TABS)throw Error('Maximum 20 tabs');const t=newTab(generatedName(w));return {...w,tabs:[...w.tabs,t],activeTabId:t.id};}
 case 'duplicateTab':{if(w.tabs.length>=MAX_TABS)throw Error('Maximum 20 tabs');const source=w.tabs.find(t=>t.id===a.id);if(!source)return w;const t=structuredClone(source);t.id=uuid();t.name=`${source.name.slice(0,70)} copy`;t.charts=t.charts.map(c=>({...c,id:uuid()}));return {...w,tabs:[...w.tabs,t],activeTabId:t.id};}
 case 'selectTab':return w.tabs.some(t=>t.id===a.id)?{...w,activeTabId:a.id}:w;
 case 'renameTab':{const name=a.name.trim();if(!name||name.length>80)throw Error('Tab names must contain 1–80 characters');return {...w,tabs:w.tabs.map(t=>t.id===a.id?{...t,name}:t)};}
 case 'removeTab':{if(w.tabs.length===1)throw Error('Keep at least one tab');const index=w.tabs.findIndex(t=>t.id===a.id);const tabs=w.tabs.filter(t=>t.id!==a.id);return {...w,tabs,activeTabId:w.activeTabId===a.id?tabs[Math.max(0,index-1)].id:w.activeTabId};}
 case 'reorderTab':return {...w,tabs:reorder(w.tabs,a.from,a.to)};
 case 'grid':{if(!presets.some(([r,c])=>r===a.rows&&c===a.columns))throw Error('Unsupported grid');if(a.rows*a.columns<current.charts.length)throw Error('Remove charts before choosing a smaller grid');if(current.grid.rows===a.rows&&current.grid.columns===a.columns)return w;return change({...current,grid:grid(a.rows,a.columns)});}
 case 'tracks':{const count=a.axis==='rowFractions'?current.grid.rows:current.grid.columns;const sum=a.values.reduce((s,x)=>s+x,0);if(a.values.length!==count||a.values.some(x=>!Number.isFinite(x)||x<=0)||sum<=0)throw Error('Invalid track sizes');return change({...current,grid:{...current.grid,[a.axis]:a.values.map(x=>x/sum)}});}
 case 'addChart':{if(current.charts.length>=MAX_CHARTS)throw Error('Maximum 9 charts per tab');const source=current.charts.find(c=>c.id===a.sourceId);const charts=[...current.charts,newChart(source)];let g=current.grid;if(g.rows*g.columns<charts.length){const p=([[1,1],[1,2],[2,2],[2,3],[3,3]] as const).find(([r,c])=>r*c>=charts.length)!;g=grid(...p);}return change({...current,charts,grid:g});}
 case 'removeChart':if(current.charts.length===1)throw Error('Keep at least one chart');return change({...current,charts:current.charts.filter(c=>c.id!==a.id)});
 case 'reorderChart':return change({...current,charts:reorder(current.charts,a.from,a.to)});
 case 'chart':return change({...current,charts:current.charts.map(c=>c.id===a.id?{...c,...a.patch,id:c.id}:c)});
 case 'settings':return {...w,...(a.theme?{theme:a.theme}:{}),...(a.displayTimezone?{displayTimezone:a.displayTimezone}:{})};
 }
}
