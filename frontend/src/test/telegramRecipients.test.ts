import {expect,it} from 'vitest';
import {telegramRecipients} from '../workspace/telegramRecipients';

it('normalizes comma-separated private recipients while preserving first username spelling and order',()=>{
 expect(telegramRecipients(' 42, @Alice, +0042 , @ALICE, @bob, 00073 ')).toEqual(['42','@Alice','@bob','73']);
 expect(telegramRecipients(' \t ')).toEqual([]);
 expect(telegramRecipients('\u0085 42 \u0085')).toEqual(['42']);
 expect(telegramRecipients('\u0085')).toEqual([]);
 expect(telegramRecipients('\uFEFF@alice')).toBeNull();
 expect(telegramRecipients('@legacy_user')).toEqual(['@legacy_user']);
 expect(telegramRecipients('4503599627370495')).toEqual(['4503599627370495']);
});

it('rejects incomplete lists and non-private or unsafe chat identities',()=>{
 for(const value of [',','42,',' ,42','42,,@alice','-42','0','+0','4503599627370496','1.5','1e3','@bad name','@','@bad/name','42, @bad name',String.fromCharCode(0),'@用户'])expect(telegramRecipients(value),value).toBeNull();
});

it('limits distinct recipients and input bytes, allowing harmless duplicate entries',()=>{
 const recipients=Array.from({length:20},(_,index)=>`@person_${index}`);
 expect(telegramRecipients(recipients.join(','))).toEqual(recipients);
 expect(telegramRecipients([...recipients,'@PERSON_0'].join(', '))).toEqual(recipients);
 expect(telegramRecipients([...recipients,'21'].join(','))).toBeNull();
 expect(telegramRecipients('@alice'+','.repeat(4091))).toBeNull();
 expect(telegramRecipients('\u2003'.repeat(1400))).toBeNull();
});
