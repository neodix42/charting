import {test,expect,type Locator,type Page} from '@playwright/test';
import {THEME_OPTIONS,getThemePalette,type ThemeName} from '../src/workspace/themes';

const rgb=(hex:string)=>`rgb(${[1,3,5].map(offset=>parseInt(hex.slice(offset,offset+2),16)).join(', ')})`;
async function changeTheme(page:Page,theme:ThemeName){
 await page.getByRole('button',{name:'Open settings',exact:true}).click();
 await page.getByLabel('Theme',{exact:true}).selectOption(theme);
 await page.getByRole('button',{name:'Close settings',exact:true}).click();
 await expect.poll(()=>page.evaluate(()=>document.documentElement.dataset.theme)).toBe(theme);
 // Keep hover feedback separate from the button's normal fill.
 await page.mouse.move(0,0);
}

/** Read pixels from the real painted widget, including WebKit progress pseudo-elements. */
async function progressPixels(progress:Locator){
 const screenshot=(await progress.screenshot()).toString('base64');
 return progress.page().evaluate(async encoded=>{
  const bytes=Uint8Array.from(atob(encoded),character=>character.charCodeAt(0)),image=await createImageBitmap(new Blob([bytes],{type:'image/png'}));
  const canvas=document.createElement('canvas');canvas.width=image.width;canvas.height=image.height;
  const context=canvas.getContext('2d')!;context.drawImage(image,0,0);image.close();
  const sample=(fraction:number)=>{const pixel=context.getImageData(Math.floor(canvas.width*fraction),Math.floor(canvas.height/2),1,1).data;return `#${[...pixel].slice(0,3).map(value=>value.toString(16).padStart(2,'0')).join('')}`;};
  return{fill:sample(.25),track:sample(.75)};
 },screenshot);
}

for(const screen of ['Downloader','Enrichment','Analysis'] as const){
 test(`${screen} buttons and painted progress bars follow all 25 theme transitions`,async({page})=>{
  await page.goto('/');
  await page.evaluate(()=>{
   (window as any).__testSetEnrichmentFiles([{id:'theme-file',name:'btcusd-d1-theme.csv',symbol:'btcusd',timeframe:'d1',fields:['timestamp','open','high','low','close','volume'],bytes:4096}]);
   (window as any).__testSetScreener({jobs:[{id:'theme-download',symbol:'btcusd',timeframe:'d1',from:'2024-01-01',to:'2024-01-02',status:'failed',progress:50,message:'Explicit appearance fixture: interrupted download'}]});
   (window as any).__testSetEnrichment({active:false,total:1,completed:0,failed:0,cancelled:1,progress:50,jobs:[]});
   (window as any).__testSetAnalysisInfo({active:false,phase:'cancelled',total:2,completed:1,failed:0,currentFile:'',progress:50,rows:100,totalMatches:0,matchedFiles:0,results:[]});
  });
  await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:screen,exact:true}).click();
  if(screen==='Downloader')await page.getByRole('checkbox',{name:'Select all symbols',exact:true}).check();
  const button=screen==='Downloader'?page.getByRole('button',{name:'Download 4 symbols',exact:true}):page.getByRole('button',{name:screen==='Enrichment'?'Refresh data and indicators':'Apply conditions',exact:true});
  const progress=screen==='Downloader'?page.locator('.screener-job progress'):page.getByRole('progressbar',{name:screen==='Enrichment'?'Overall enrichment progress':'Analysis scan progress',exact:true});
  await expect(button).toBeEnabled();await expect(progress).toHaveAttribute('value','50');
  for(const source of THEME_OPTIONS){
   await changeTheme(page,source.id);
   for(const target of THEME_OPTIONS){
    await changeTheme(page,target.id);const palette=getThemePalette(target.id);
    await expect(button).toHaveCSS('background-color',rgb(palette.accent));
    await expect(button).toHaveCSS('border-top-color',rgb(palette.accent));
    await expect(button).toHaveCSS('color',target.id==='light'?'rgb(255, 255, 255)':'rgb(19, 23, 15)');
    await expect(progress).toHaveCSS('appearance','none');
    expect(await progressPixels(progress),`${source.label} → ${target.label}`).toEqual({fill:palette.accent,track:palette.hover});
    await changeTheme(page,source.id);
   }
  }
 });
}
