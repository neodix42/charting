import type {DrawingType} from '../types';
import {MAX_DRAWINGS} from '../workspace/model';

const tools:{type:DrawingType;label:string;path:string}[]=[
 {type:'arrowUp',label:'Arrow up',path:'M12 21V3M5 10l7-7 7 7'},
 {type:'arrowDown',label:'Arrow down',path:'M12 3v18M5 14l7 7 7-7'},
 {type:'horizontal',label:'Horizontal line',path:'M3 12h18'},
 {type:'trendline',label:'Trendline',path:'M4 20 20 4M3 17v4h4M17 3h4v4'},
];
interface Props {tool:DrawingType|null;moving:boolean;resizing:boolean;selected:boolean;selectedType?:DrawingType;count:number;total:number;onDelete():void;onTool(tool:DrawingType|null):void;onUndo():void;onClear():void}
export function DrawingToolbar({tool,moving,resizing,selected,selectedType,count,total,onDelete,onTool,onUndo,onClear}:Props){
 const limit=total>=MAX_DRAWINGS;
 const hint=resizing?'Adjust endpoint · release to save · Esc cancels':moving?'Move drawing · click to place · Esc cancels':tool==='trendline'?'Click start, then end · Esc cancels':tool?'Click chart to place · Esc cancels':selected?(selectedType==='trendline'||selectedType==='horizontal'?'Drag an endpoint · Del deletes · Esc clears':'Del deletes · double-click to move · Esc clears'):limit?'100 drawings · click to select':'Click to select · double-click to move';
 return <div className="drawing-toolbar" role="group" aria-label="Drawing tools">
  <span className="drawing-label">Draw</span>
  <button type="button" className={`drawing-tool ${tool===null&&!moving&&!selected?'selected':''}`} aria-label="Cursor" aria-pressed={tool===null&&!moving&&!selected} title="Cursor / clear selection or cancel editing (Esc)" onClick={()=>onTool(null)}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m5 3 14 10-7 1-3 7Z"/></svg></button>
  {tools.map(item=><button key={item.type} type="button" className={`drawing-tool ${tool===item.type?'selected':''}`} aria-label={item.label} aria-pressed={tool===item.type} disabled={limit} title={limit?'Maximum 100 drawings per chart':`${item.label}${item.type==='trendline'?' · click start and end':' · click to place'}`} onClick={()=>onTool(tool===item.type?null:item.type)}><svg viewBox="0 0 24 24" aria-hidden="true"><path d={item.path}/></svg></button>)}
  <span className="drawing-separator"/>
  <button type="button" className="drawing-delete" aria-label="Delete selected drawing" title="Delete selected drawing (Del)" disabled={!selected} onClick={onDelete}>Delete</button>
  <button type="button" className="drawing-tool" aria-label="Undo drawing" title="Remove the last drawing on this instrument" disabled={!count} onClick={onUndo}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5 3 10l5 5M3 10h11a6 6 0 0 1 0 12"/></svg></button>
  <button type="button" className="drawing-tool" aria-label="Clear drawings" title="Clear drawings on this instrument" disabled={!count} onClick={onClear}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 6h16M9 6V3h6v3M6 6l1 15h10l1-15M10 10v7M14 10v7"/></svg></button>
  <span className="drawing-count" aria-label="Drawing count">{count}</span>
  <span className="drawing-hint" aria-live="polite">{hint}</span>
 </div>;
}
