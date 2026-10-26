import {ColorType} from 'lightweight-charts';
import type {ThemePalette} from '../workspace/themes';

/** Canvas charts need explicit colors; update these options without replacing their feeds or viewports. */
export function chartThemeOptions(palette:ThemePalette){
 return {
  layout:{background:{type:ColorType.Solid as const,color:palette.chartBackground},textColor:palette.chartText,panes:{separatorColor:palette.chartBorder,separatorHoverColor:palette.hover}},
  grid:{vertLines:{color:palette.chartGrid},horzLines:{color:palette.chartGrid}},
  crosshair:{vertLine:{color:palette.chartCrosshair,labelBackgroundColor:palette.elevated},horzLine:{color:palette.chartCrosshair,labelBackgroundColor:palette.elevated}},
  rightPriceScale:{borderColor:palette.chartBorder},
  timeScale:{borderColor:palette.chartBorder},
 };
}
