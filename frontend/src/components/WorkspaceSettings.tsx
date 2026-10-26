import {useRef,useState,type KeyboardEvent} from 'react';
import type {Backend,ChartRightGap,Workspace} from '../types';
import {errorText} from '../types';
import {DEFAULT_CHART_RIGHT_GAP,type Action} from '../workspace/model';
import {DEFAULT_FONT_SIZE,MIN_FONT_SIZE,MAX_FONT_SIZE} from '../workspace/appearance';
import {THEME_OPTIONS} from '../workspace/themes';
import {ChartGapSetting} from './ChartGapSetting';
import {CandleStorageSetting} from './CandleStorageSetting';
import {ScreenerDirectorySetting} from './ScreenerDirectorySetting';

const pages=['Appearance','Storage','Screener','Export','Debug'] as const;
type SettingsPage=typeof pages[number];
interface Props {workspace:Workspace;api:Backend;onClose():void;onChange(action:Action):void;onChartGap(gap:ChartRightGap):void;onNative(action:'export'|'import'):Promise<void>;onReset():void;onDiagnostics():Promise<Record<string,unknown>>;onError(message:string):void}

export function WorkspaceSettings({workspace,api,onClose,onChange,onChartGap,onNative,onReset,onDiagnostics,onError}:Props){
 const [page,setPage]=useState<SettingsPage>('Appearance'),[zone,setZone]=useState(workspace.displayTimezone),[diagnostics,setDiagnostics]=useState<Record<string,unknown>|null>(null);
 const tabs=useRef<Array<HTMLButtonElement|null>>([]);
 const timezones=Array.from(new Set(['UTC','system',Intl.DateTimeFormat().resolvedOptions().timeZone,'America/New_York','Europe/London','Europe/Berlin','Asia/Dubai','Asia/Tokyo','Asia/Singapore','Australia/Sydney']));
 const selectPage=(event:KeyboardEvent<HTMLButtonElement>,index:number)=>{
  const target=event.key==='ArrowRight'?(index+1)%pages.length:event.key==='ArrowLeft'?(index+pages.length-1)%pages.length:event.key==='Home'?0:event.key==='End'?pages.length-1:-1;
  if(target<0)return;event.preventDefault();setPage(pages[target]);tabs.current[target]?.focus();
 };
 return <div className="modal-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget)onClose();}}><section className="modal settings-modal" role="dialog" aria-modal="true" aria-label="Workspace settings">
  <div className="modal-header"><h2>Workspace settings</h2><button aria-label="Close settings" onClick={onClose}>×</button></div>
  <nav className="settings-tabs" role="tablist" aria-label="Settings pages">{pages.map((name,index)=><button key={name} ref={element=>{tabs.current[index]=element;}} id={`settings-tab-${name.toLowerCase()}`} role="tab" aria-selected={page===name} aria-controls={`settings-panel-${name.toLowerCase()}`} tabIndex={page===name?0:-1} onKeyDown={event=>selectPage(event,index)} onClick={()=>setPage(name)}>{name}</button>)}</nav>
  <div className="settings-page" role="tabpanel" id={`settings-panel-${page.toLowerCase()}`} aria-labelledby={`settings-tab-${page.toLowerCase()}`}>
   {page==='Appearance'&&<>
    <label className="setting-row"><span><strong>Theme</strong><small>Your theme applies to every chart and Screener screen.</small></span><select aria-label="Theme" value={workspace.theme} onChange={event=>onChange({type:'settings',theme:event.target.value as Workspace['theme']})}>{THEME_OPTIONS.map(theme=><option key={theme.id} value={theme.id}>{theme.label}</option>)}</select></label>
    <label className="setting-row"><span><strong>Font size</strong><small>Scales text throughout the app, including chart labels and status bars.</small></span><select aria-label="Font size" value={workspace.fontSize??DEFAULT_FONT_SIZE} onChange={event=>onChange({type:'settings',fontSize:Number(event.target.value)})}>{Array.from({length:MAX_FONT_SIZE-MIN_FONT_SIZE+1},(_,index)=>index+MIN_FONT_SIZE).map(size=><option key={size} value={size}>{size} px{size===DEFAULT_FONT_SIZE?' (default)':''}</option>)}</select></label>
    <div className="setting-row"><span><strong>Display timezone</strong><small>Candle boundaries always use UTC.</small></span><div className="timezone-controls"><input aria-label="Display timezone" value={zone} list="timezones" onChange={event=>setZone(event.target.value)}/><datalist id="timezones">{timezones.map(value=><option key={value} value={value}/>)}</datalist><button onClick={()=>{try{if(zone!=='system')new Intl.DateTimeFormat('en',{timeZone:zone}).format();const displayTimezone=zone==='system'?Intl.DateTimeFormat().resolvedOptions().timeZone:zone;onChange({type:'settings',displayTimezone});setZone(displayTimezone);}catch{onError('Enter a valid IANA timezone, UTC or system');}}}>Apply</button></div></div>
    <ChartGapSetting value={workspace.chartRightGap??DEFAULT_CHART_RIGHT_GAP} onApply={onChartGap}/>
   </>}
   {page==='Storage'&&<CandleStorageSetting api={api}/>}
   {page==='Screener'&&<ScreenerDirectorySetting api={api}/>}
   {page==='Export'&&<div className="settings-section"><h3>Portable workspace</h3><p>Export chart settings, appearance and layout as JSON. Import replaces the workspace after validation and confirmation, with a backup of the current state.</p><div className="button-row"><button onClick={()=>void onNative('export')}>Export JSON…</button><button onClick={()=>void onNative('import')}>Import JSON…</button><button className="danger" onClick={onReset}>Reset workspace…</button></div></div>}
   {page==='Debug'&&<><div className="settings-section"><h3>Diagnostics</h3><p>Check active resources and backend health. Inactive tabs retain configuration and release their market leases.</p><button onClick={()=>void onDiagnostics().then(setDiagnostics).catch(error=>onError(errorText(error)))}>Refresh diagnostics</button>{diagnostics&&<pre className="diagnostics">{JSON.stringify(diagnostics,null,2)}</pre>}</div><div className="settings-section attribution"><strong>Lightweight Charts™ by TradingView</strong><p>This independent application uses public market data. Market access and freshness depend on the provider and network. No exchange account or trading API credentials are used.</p><button className="text-button" onClick={()=>void api.OpenTradingView().catch(error=>onError(errorText(error)))}>TradingView ↗</button></div></>}
  </div>
 </section></div>;
}
