/** The same names are persisted in workspace JSON and used by root theme selectors. */
export const THEME_OPTIONS=[
 {id:'dark',label:'Dark'},
 {id:'light',label:'Light'},
 {id:'matrix',label:'Matrix'},
 {id:'cyberpunk',label:'Cyberpunk'},
 {id:'red-sands',label:'Red Sands'},
] as const;
export type ThemeName=typeof THEME_OPTIONS[number]['id'];

export interface ThemePalette {
 readonly scheme:'dark'|'light';
 readonly bg:string;readonly panel:string;readonly elevated:string;
 readonly text:string;readonly muted:string;readonly border:string;
 readonly accent:string;readonly hover:string;readonly danger:string;readonly up:string;readonly shadow:string;
 readonly chartBackground:string;readonly chartGrid:string;readonly chartText:string;readonly chartBorder:string;readonly chartCrosshair:string;
}

/** Chart canvases cannot inherit CSS colors; keep their palette beside the app colors. */
export const THEME_PALETTES:Readonly<Record<ThemeName,ThemePalette>>={
 dark:{scheme:'dark',bg:'#0c1117',panel:'#111820',elevated:'#18212c',text:'#dce5ed',muted:'#8191a2',border:'#293440',accent:'#e7ad51',hover:'#232f3d',danger:'#ed737c',up:'#34bea0',shadow:'0 14px 65px #00000088',chartBackground:'#111820',chartGrid:'#1c2631',chartText:'#8191a2',chartBorder:'#293440',chartCrosshair:'#566578'},
 light:{scheme:'light',bg:'#eef2f5',panel:'#ffffff',elevated:'#f1f4f6',text:'#263544',muted:'#5f7081',border:'#d7dfe6',accent:'#94621f',hover:'#e5ebf0',danger:'#bd4554',up:'#157b65',shadow:'0 14px 65px #18324b26',chartBackground:'#ffffff',chartGrid:'#edf0f3',chartText:'#5f7081',chartBorder:'#d7dfe6',chartCrosshair:'#8795a3'},
 matrix:{scheme:'dark',bg:'#020503',panel:'#071008',elevated:'#0e1a10',text:'#d9ffd9',muted:'#8fbb91',border:'#285431',accent:'#94f744',hover:'#18351b',danger:'#ff8294',up:'#9df75d',shadow:'0 14px 65px #000000bb',chartBackground:'#050c06',chartGrid:'#16351d',chartText:'#8fbb91',chartBorder:'#285431',chartCrosshair:'#a0d187'},
 cyberpunk:{scheme:'dark',bg:'#130c24',panel:'#1c1234',elevated:'#281c44',text:'#f6ecff',muted:'#c5a8df',border:'#5d3b83',accent:'#24e2f3',hover:'#3a2559',danger:'#ff6ba7',up:'#4ce2bb',shadow:'0 14px 65px #080018bb',chartBackground:'#180f2b',chartGrid:'#3c2555',chartText:'#c5a8df',chartBorder:'#5d3b83',chartCrosshair:'#cb91f9'},
 'red-sands':{scheme:'dark',bg:'#251411',panel:'#321d17',elevated:'#40271d',text:'#fff0db',muted:'#d0ad94',border:'#74482f',accent:'#ffc180',hover:'#593523',danger:'#ff8d83',up:'#b9d28a',shadow:'0 14px 65px #170600bb',chartBackground:'#2a1813',chartGrid:'#513224',chartText:'#d0ad94',chartBorder:'#74482f',chartCrosshair:'#dbb492'},
};

export const isThemeName=(value:string):value is ThemeName=>Object.hasOwn(THEME_PALETTES,value);
export const getThemePalette=(theme:string):ThemePalette=>isThemeName(theme)?THEME_PALETTES[theme]:THEME_PALETTES.dark;
