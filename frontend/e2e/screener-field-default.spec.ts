import {test,expect} from '@playwright/test';
import {openTestApp,reloadTestApp} from './helpers/testApp';
import {setAnalysisRightConstant} from './helpers/analysis';

test('fresh and added Screener rows default to Field while saved Constant operands retain their values',async({page})=>{
 await openTestApp(page);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();
 await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 const type=(row:number)=>page.getByLabel(`Condition ${row} right type`,{exact:true});
 const field=(row:number)=>page.getByLabel(`Condition ${row} right field`,{exact:true});
 await expect(type(1)).toHaveValue('field');await expect(field(1)).toHaveValue('close');
 await page.getByRole('button',{name:'Add condition',exact:false}).click();
 await expect(type(2)).toHaveValue('field');await expect(field(2)).toHaveValue('close');
 await setAnalysisRightConstant(page,1,'42');
 await page.getByRole('button',{name:'Save as new',exact:true}).click();
 await page.getByLabel('Condition name',{exact:true}).fill('Retained numeric value');
 const dialog=page.getByRole('dialog',{name:'Save condition',exact:true});
 await dialog.getByRole('button',{name:'Save condition',exact:true}).click();await expect(dialog).toHaveCount(0);
 await type(1).selectOption('field');
 await page.getByRole('button',{name:'Load condition Retained numeric value',exact:true}).click();
 await expect(type(1)).toHaveValue('number');await expect(page.getByLabel('Condition 1 right constant',{exact:true})).toHaveValue('42');
 await expect(type(2)).toHaveValue('field');await expect(field(2)).toHaveValue('close');
 await reloadTestApp(page);
 await page.getByRole('tab',{name:'Screener',exact:true}).click();await page.getByRole('tab',{name:'Analysis',exact:true}).click();
 await expect(type(1)).toHaveValue('number');await expect(page.getByLabel('Condition 1 right constant',{exact:true})).toHaveValue('42');
 await page.getByRole('button',{name:'Add condition',exact:false}).click();
 await expect(type(3)).toHaveValue('field');await expect(field(3)).toHaveValue('close');
});
