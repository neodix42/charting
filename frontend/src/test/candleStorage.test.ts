import {describe,it,expect} from 'vitest';
import {folderSize} from '../components/CandleStorageSetting';
describe('candle folder size display',()=>{
 it('distinguishes empty storage, byte sizes and larger binary units',()=>{expect(folderSize(0)).toBe('0 B');expect(folderSize(1023)).toBe('1,023 B');expect(folderSize(1024)).toBe('1 KiB');expect(folderSize(1572864)).toBe('1.5 MiB');expect(folderSize(1024**4)).toBe('1 TiB');});
 it('does not present invalid or unknown sizes as an empty folder',()=>{expect(folderSize(-1)).toBe('Unavailable');expect(folderSize(NaN)).toBe('Unavailable');expect(folderSize(Infinity)).toBe('Unavailable');});
});
