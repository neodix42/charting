import {createContext,useContext} from 'react';

export const DEFAULT_FONT_SIZE=15,MIN_FONT_SIZE=10,MAX_FONT_SIZE=24;
export const AppearanceFontSize=createContext(DEFAULT_FONT_SIZE);
export const useAppearanceFontSize=()=>useContext(AppearanceFontSize);
/** Match the existing rem text proportions, whose design baseline remains 13px. */
const DESIGN_FONT_SIZE=13;
export const chartFontSize=(base:number,fontSize=DEFAULT_FONT_SIZE)=>Math.max(1,Math.round(base*fontSize/DESIGN_FONT_SIZE));
