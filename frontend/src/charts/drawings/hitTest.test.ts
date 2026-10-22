import {describe,expect,it} from 'vitest';
import type {ProjectedDrawing} from './DrawingPrimitive';
import {hitTestDrawing} from './hitTest';

const drawing=(id:string,type:ProjectedDrawing['type'],coordinates:ProjectedDrawing['coordinates']):ProjectedDrawing=>({id,type,coordinates,points:[]});
describe('drawing selection geometry',()=>{
 it('selects arrow tips and shafts in either direction without a large invisible hit area',()=>{
  const up=drawing('up','arrowUp',[{x:30,y:20}]),down=drawing('down','arrowDown',[{x:70,y:70}]);
  for(const point of [{x:30,y:20},{x:30,y:43},{x:70,y:70},{x:70,y:47}])expect(hitTestDrawing([up,down],point,100,100)).toBe(point.x===30?'up':'down');
  expect(hitTestDrawing([up],{x:45,y:40},100,100)).toBeNull();
  expect(hitTestDrawing([up],{x:30,y:65},100,100)).toBeNull();
 });
 it('hits pane-wide horizontal strokes and only the finite visible part of a trendline',()=>{
  const horizontal=drawing('horizontal','horizontal',[{x:-500,y:35}]);
  expect(hitTestDrawing([horizontal],{x:95,y:40},100,100)).toBe('horizontal');
  expect(hitTestDrawing([horizontal],{x:95,y:42},100,100)).toBeNull();
  const trend=drawing('trend','trendline',[{x:20,y:20},{x:60,y:60}]);
  expect(hitTestDrawing([trend],{x:40,y:43},100,100)).toBe('trend');
  expect(hitTestDrawing([trend],{x:85,y:85},100,100)).toBeNull();
  const clipped=drawing('clipped','trendline',[{x:-50,y:-50},{x:150,y:150}]);
  expect(hitTestDrawing([clipped],{x:40,y:40},100,100)).toBe('clipped');
 });
 it('prefers the topmost painted item and ignores hidden items, panes and invalid coordinates',()=>{
  const first=drawing('first','horizontal',[{x:25,y:40}]),last=drawing('last','trendline',[{x:20,y:40},{x:80,y:40}]);
  expect(hitTestDrawing([first,last],{x:40,y:40},100,100)).toBe('last');
  expect(hitTestDrawing([first],{x:40,y:101},100,100)).toBeNull();
  expect(hitTestDrawing([drawing('hidden','horizontal',[{x:25,y:-3}])],{x:20,y:0},100,100)).toBeNull();
  expect(hitTestDrawing([drawing('hidden','arrowUp',[{x:25,y:-40}])],{x:25,y:0},100,100)).toBeNull();
  expect(hitTestDrawing([drawing('invalid','horizontal',[{x:25,y:NaN}])],{x:25,y:25},100,100)).toBeNull();
 });
});
