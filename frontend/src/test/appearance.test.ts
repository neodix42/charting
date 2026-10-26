import {describe,it,expect} from 'vitest';
import {DEFAULT_FONT_SIZE,chartFontSize} from '../workspace/appearance';
import {firstWorkspace,reduceWorkspace} from '../workspace/model';

describe('persistent global appearance size',()=>{
 it('retains the existing default and scales canvas text proportionally',()=>{
  expect(firstWorkspace().fontSize).toBe(13);expect(DEFAULT_FONT_SIZE).toBe(13);
  expect(chartFontSize(11)).toBe(11);expect(chartFontSize(10,26)).toBe(20);expect(chartFontSize(11,20)).toBe(17);
 });
 it('changes only appearance while preserving chart identities, history and hidden tabs',()=>{
  let workspace=firstWorkspace();workspace=reduceWorkspace(workspace,{type:'addTab'});workspace.tabs[0].charts[0].view={mode:'historical',fromSec:100,toSec:1000,barSpacing:7,rightOffset:0};
  const before=structuredClone(workspace),changed=reduceWorkspace(workspace,{type:'settings',fontSize:20});
  expect(changed.fontSize).toBe(20);expect(changed.tabs).toBe(workspace.tabs);expect({...changed,fontSize:workspace.fontSize}).toEqual(before);
  expect(JSON.parse(JSON.stringify(changed)).fontSize).toBe(20);expect(workspace).toEqual(before);
 });
 it('leaves legacy omission intact until explicitly changed and rejects invalid values atomically',()=>{
  const legacy=firstWorkspace();delete legacy.fontSize;
  expect(reduceWorkspace(legacy,{type:'settings',theme:'light'})).not.toHaveProperty('fontSize');
  const before=structuredClone(legacy);
  for(const fontSize of [NaN,Infinity,0,9,25,13.5])expect(()=>reduceWorkspace(legacy,{type:'settings',fontSize})).toThrow('Font size');
  expect(legacy).toEqual(before);
  for(const fontSize of [10,24])expect(reduceWorkspace(legacy,{type:'settings',fontSize}).fontSize).toBe(fontSize);
 });
});
