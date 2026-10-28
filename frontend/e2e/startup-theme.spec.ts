import {test,expect} from '@playwright/test';
import {firstWorkspace} from '../src/workspace/model';

test('production HTML is light before JavaScript and stylesheets load, even on a dark desktop',async({browser})=>{
 const context=await browser.newContext({javaScriptEnabled:false,colorScheme:'dark'});
 try{
  const page=await context.newPage();
  await page.route('**/*.css',route=>route.abort());
  await page.goto('http://127.0.0.1:4174/');
  await expect(page.locator('html')).toHaveAttribute('data-theme','light');
  await expect(page.locator('html')).toHaveCSS('background-color','rgb(238, 242, 245)');
  await expect(page.locator('html')).toHaveCSS('color','rgb(38, 53, 68)');
  await expect(page.locator('html')).toHaveCSS('color-scheme','light');
 }finally{await context.close();}
});

for(const savedTheme of [null,'dark','matrix','cyberpunk','red-sands'] as const){
 test(`loading screen stays light until ${savedTheme??'a new light workspace'} loads`,async({page})=>{
  const workspace=firstWorkspace();if(savedTheme)workspace.theme=savedTheme;
  await page.addInitScript(value=>{
   if(value)sessionStorage.setItem('cryptodesk-explicit-test-workspace',JSON.stringify(value));
   let release!:()=>void;
   const ready=new Promise<void>(resolve=>{release=resolve;});
   (window as any).__releaseWorkspace=()=>release();
   let bridge:any;
   Object.defineProperty(window,'go',{
    configurable:true,get:()=>bridge,set:value=>{
     bridge=value;const load=bridge.main.App.LoadWorkspace;
     bridge.main.App.LoadWorkspace=async()=>{await ready;return load();};
    },
   });
  },savedTheme?workspace:null);
  await page.goto('/');
  await expect(page.getByText('Opening saved workspace…',{exact:true})).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('data-theme','light');
  await expect(page.locator('html')).toHaveCSS('background-color','rgb(238, 242, 245)');
  await expect(page.locator('html')).toHaveCSS('color-scheme','light');
  await page.evaluate(()=>(window as any).__releaseWorkspace());
  await expect(page.getByTestId('chart-cell')).toHaveCount(1);
  await expect(page.locator('html')).toHaveAttribute('data-theme',savedTheme??'light');
  await page.getByRole('button',{name:'Open settings',exact:true}).click();
  await expect(page.getByLabel('Theme',{exact:true})).toHaveValue(savedTheme??'light');
 });
}
