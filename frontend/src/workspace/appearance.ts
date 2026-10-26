import {createContext,useContext} from 'react';

export const DEFAULT_FONT_SIZE=13,MIN_FONT_SIZE=10,MAX_FONT_SIZE=24;
export const AppearanceFontSize=createContext(DEFAULT_FONT_SIZE);
export const useAppearanceFontSize=()=>useContext(AppearanceFontSize);
/** Canvas labels preserve their original proportions alongside rem-based UI text. */
export const chartFontSize=(base:number,fontSize=DEFAULT_FONT_SIZE)=>Math.max(1,Math.round(base*fontSize/DEFAULT_FONT_SIZE));
