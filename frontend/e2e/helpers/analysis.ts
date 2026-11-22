import type {Page} from '@playwright/test';

/** Numeric scenarios explicitly choose a Constant rather than relying on row defaults. */
export async function setAnalysisRightConstant(page:Page,condition:number,value:string){
 const type=page.getByLabel(`Condition ${condition} right type`,{exact:true});
 if(await type.inputValue()!=='number')await type.selectOption('number');
 await page.getByLabel(`Condition ${condition} right constant`,{exact:true}).fill(value);
}
