import {createRoot} from 'react-dom/client';
import {AnalysisPlot} from '../components/ScreenerAnalysisChart';
import type {AnalysisChart} from '../workspace/screenerAnalysis';
import {analysisDate} from '../workspace/screenerAnalysis';
import {AppearanceFontSize,DEFAULT_FONT_SIZE,MIN_FONT_SIZE,MAX_FONT_SIZE} from '../workspace/appearance';
import {getThemePalette,isThemeName} from '../workspace/themes';
import '../components/ScreenerAnalysis.css';
import './chartSnapshot.css';

interface SnapshotRequest {chart:AnalysisChart;title:string;source:string;market:string;condition:string;theme?:string;fontSize?:number}

/** Called only inside the network-isolated local Chromium renderer. */
async function renderSignalChart(request:SnapshotRequest){
 const {chart}=request;
 if(!chart||!Array.isArray(chart.bars)||!chart.bars.length||chart.bars.length>121||!Array.isArray(chart.fields)||chart.fields.length>64||!Number.isFinite(chart.matchTime)||!chart.bars.some(bar=>bar.time===chart.matchTime))throw Error('Invalid signal chart snapshot');
 if(chart.bars.some(bar=>!Number.isFinite(bar.time)||bar.time>chart.matchTime||!bar.values||Object.values(bar.values).some(value=>!Number.isFinite(value))))throw Error('Invalid signal chart values');
 const theme=request.theme&&isThemeName(request.theme)?request.theme:'light',palette=getThemePalette(theme);
 const fontSize=Number.isFinite(request.fontSize)?Math.max(MIN_FONT_SIZE,Math.min(MAX_FONT_SIZE,Math.round(request.fontSize!))):DEFAULT_FONT_SIZE;
 document.documentElement.dataset.theme=theme;
 document.documentElement.style.fontSize=`${fontSize}px`;
 for(const key of ['bg','panel','text','muted','border','accent','danger','up'] as const)document.documentElement.style.setProperty(`--${key}`,palette[key]);
 const host=document.getElementById('root');if(!host)throw Error('Missing chart host');
 createRoot(host).render(<AppearanceFontSize value={fontSize}><article className="signal-chart" aria-label="Signal chart">
  <header><div><strong className="signal-chart-brand">CryptoDesk · SIGNAL CHART</strong><h1>{request.title||chart.symbol} <span>{chart.timeframe.toUpperCase()}</span></h1><p>{request.source}{request.market?` · ${request.market}`:''} · {analysisDate(chart.matchTime)}</p></div><div className="signal-chart-condition"><strong>Condition</strong><p>{request.condition}</p></div></header>
  <p className="signal-chart-note">Latest matched closed bar · entry and protective levels are indicative signals.</p>
  <AnalysisPlot data={chart}/>
  <footer><span>{chart.bars.length} closed bars · UTC · no future candles</span><span>Lightweight Charts™ by TradingView</span></footer>
 </article></AppearanceFontSize>);
}

Object.assign(window,{__renderSignalChart:renderSignalChart});
