import {describe,it,expect} from 'vitest';
import css from '../themes.css?raw';
import schema from '../../../schema/workspace.schema.json';
import {THEME_OPTIONS,THEME_PALETTES,getThemePalette,isThemeName,type ThemePalette} from '../workspace/themes';
import {firstWorkspace,reduceWorkspace} from '../workspace/model';
import type {Workspace} from '../types';

const luminance=(color:string)=>{
 const channels=[1,3,5].map(offset=>parseInt(color.slice(offset,offset+2),16)/255).map(value=>value<=.04045?value/12.92:((value+.055)/1.055)**2.4);
 return channels[0]*.2126+channels[1]*.7152+channels[2]*.0722;
};
const contrast=(first:string,second:string)=>{const [low,high]=[luminance(first),luminance(second)].sort((a,b)=>a-b);return (high+.05)/(low+.05);};

describe('app theme palettes and persisted choices',()=>{
 it('round-trips every selectable theme without replacing charts or changing the default',()=>{
  expect(firstWorkspace().theme).toBe('dark');
  const workspace=firstWorkspace();workspace.tabs[0].charts[0].view={mode:'historical',fromSec:100,toSec:200,barSpacing:8,rightOffset:0};
  const original=structuredClone(workspace);
  expect(THEME_OPTIONS.map(theme=>theme.id)).toEqual(schema.properties.theme.enum);
  for(const {id} of THEME_OPTIONS){
   const changed=reduceWorkspace(workspace,{type:'settings',theme:id});
   expect(changed.theme).toBe(id);expect(changed.tabs).toBe(workspace.tabs);
   expect(JSON.parse(JSON.stringify(changed))).toEqual({...original,theme:id});
  }
  expect(workspace).toEqual(original);
 });
 it('rejects unknown persisted names in a settings update and keeps rendering fallback safe',()=>{
  const workspace=firstWorkspace(),original=structuredClone(workspace);
  for(const theme of ['blue','','__proto__','Matrix']){
   expect(isThemeName(theme)).toBe(false);
   expect(()=>reduceWorkspace(workspace,{type:'settings',theme:theme as Workspace['theme']})).toThrow('Unknown theme');
   expect(getThemePalette(theme)).toBe(THEME_PALETTES.dark);
  }
  expect(workspace).toEqual(original);
 });
 it('keeps CSS surfaces and chart canvas colors aligned for each theme',()=>{
  const variables:Record<string,keyof ThemePalette>={bg:'bg',panel:'panel',elevated:'elevated',text:'text',muted:'muted',border:'border',accent:'accent',hover:'hover',danger:'danger',up:'up',shadow:'shadow','chart-background':'chartBackground','chart-grid':'chartGrid','chart-text':'chartText','chart-border':'chartBorder','chart-crosshair':'chartCrosshair'};
  for(const {id} of THEME_OPTIONS){
   const palette=getThemePalette(id),block=css.match(new RegExp(`:root\\[data-theme=${id}\\]\\{([^}]+)\\}`))?.[1];
   expect(block).toBeDefined();expect(block).toContain(`color-scheme:${palette.scheme}`);
   for(const [variable,key] of Object.entries(variables))expect(block).toContain(`--${variable}:${palette[key]};`);
  }
 });
 it('keeps text, status colors and chart labels readable on every palette',()=>{
  for(const {id} of THEME_OPTIONS){
   const palette=getThemePalette(id);
   for(const background of [palette.panel,palette.elevated])for(const foreground of [palette.text,palette.muted,palette.accent,palette.up,palette.danger])expect(contrast(foreground,background),`${id}: ${foreground} on ${background}`).toBeGreaterThanOrEqual(4.5);
   expect(contrast(palette.chartText,palette.chartBackground),`${id} chart labels`).toBeGreaterThanOrEqual(4.5);
   expect(contrast(id==='light'?'#ffffff':'#13170f',palette.accent),`${id} primary action`).toBeGreaterThanOrEqual(4.5);
  }
 });
});
