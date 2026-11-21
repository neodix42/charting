package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.dukascopy.api.system.*;
import com.google.gson.*;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.atomic.*;
import static cryptodesk.jforex.BridgeTest.*;

/** Hypothetical P/L metadata uses SDK conversion only; no broker orders. */
public final class BridgeValuationTest {
    static void near(double actual,double expected,String message){check(Math.abs(actual-expected)<1e-10,message+": "+actual+" != "+expected);}
    static JsonObject estimate(Bridge bridge,Instrument instrument,String side){JsonObject r=request("tradeEstimate");r.addProperty("instrument",instrument.name());r.addProperty("side",side);return good(bridge,r).getAsJsonObject("result");}
    static ITick tick(double bid,double ask,long at){return fake(ITick.class,(method,args)->switch(method){case "getTime"->at;case "getBid"->bid;case "getAsk"->ask;case "getAskVolume"->0.01;case "getBidVolume"->0.02;default->null;});}
    static void run() throws Exception {
        long now=System.currentTimeMillis();
        AtomicLong clock=new AtomicLong(now),conversionAge=new AtomicLong();
        AtomicBoolean connected=new AtomicBoolean(),conversionBlocked=new AtomicBoolean(),conversionStale=new AtomicBoolean(),entryStale=new AtomicBoolean(),accountReadFails=new AtomicBoolean(),modernCurrencyMissing=new AtomicBoolean();
        AtomicReference<ICurrency> currency=new AtomicReference<>(JFCurrency.getInstance("EUR"));
        AtomicReference<Double> equity=new AtomicReference<>(1100.0);
        AtomicReference<ITick> entryOverride=new AtomicReference<>();
        AtomicInteger conversionCalls=new AtomicInteger(),engineCalls=new AtomicInteger(),fxQuoteCalls=new AtomicInteger();
        Set<Instrument> subscribed=new HashSet<>();
        IHistory history=fake(IHistory.class,(method,args)->{
            if(!method.equals("getLastTick"))return null;
            Instrument instrument=(Instrument)args[0];
            if (!instrument.equals(Instrument.AAPLUSUSD)) fxQuoteCalls.incrementAndGet();
            long at=now-(entryStale.get()&&instrument.equals(Instrument.AAPLUSUSD)||conversionStale.get()&&instrument.equals(Instrument.EURUSD)?60000:instrument.equals(Instrument.EURUSD)?conversionAge.get():0);
            if(instrument.equals(Instrument.AAPLUSUSD))return entryOverride.get()!=null?entryOverride.get():tick(100,100.2,at);
            if(instrument.equals(Instrument.BMWDEEUR))return tick(14.8,14.82,now-72*3_600_000L);
            if(instrument.equals(Instrument.USDJPY))return tick(149.98,150,at);
            if(instrument.equals(Instrument.EURJPY))return tick(166.65,166.68,at);
            return tick(1.11,1.112,at);
        });
        JFUtils utils=fake(JFUtils.class,(method,args)->{
            if(!method.equals("convertPipToCurrency")&&!method.equals("getRate"))return null;
            conversionCalls.incrementAndGet();
            if(method.equals("getRate"))return 0.0;
            if(conversionBlocked.get())throw new JFException("explicit unavailable SDK conversion");
            Instrument instrument=(Instrument)args[0];ICurrency account=(ICurrency)args[1];
            double rate=instrument.getSecondaryJFCurrency().getCurrencyCode().equals(account.getCurrencyCode())?1:
                instrument.getSecondaryJFCurrency().getCurrencyCode().equals("JPY")?0.006:0.9;
            return instrument.getPipValue()*rate;
        });
        IAccount account=fake(IAccount.class,(method,args)->switch(method){
            case "getLeverage"->100.0;case "getAccountCurrency"->modernCurrencyMissing.get()?null:currency.get();case "getCurrency"->currency.get().getJavaCurrency();
            case "getBalance"->1000.0;case "getEquity"->equity.get();case "getUsedMargin"->{if(accountReadFails.get())throw new RuntimeException("explicit SDK failure");yield 100.0;}default->null;});
        IContext context=fake(IContext.class,(method,args)->switch(method){
            case "getHistory"->history;case "getUtils"->utils;case "getAccount"->account;
            case "getEngine"->{engineCalls.incrementAndGet();throw new AssertionError("estimate must not access order engine");}
            case "getSubscribedInstruments"->new HashSet<>(subscribed);
            case "setSubscribedInstruments"->{subscribed.clear();subscribed.addAll((Set<Instrument>)args[0]);yield null;}
            default->null;
        });
        IClient client=fake(IClient.class,(method,args)->switch(method){
            case "isConnected"->connected.get();case "connect"->{connected.set(true);yield null;}
            case "getAvailableInstruments"->Set.of(Instrument.EURUSD,Instrument.USDJPY,Instrument.EURJPY,Instrument.AAPLUSUSD,Instrument.BMWDEEUR);
            case "getSubscribedInstruments"->new HashSet<>(subscribed);case "unsubscribeInstruments"->{subscribed.removeAll((Set<Instrument>)args[0]);yield null;}
            case "startStrategy"->{((IStrategy)args[0]).onStart(context);yield 1L;}default->null;
        });
        try(Bridge bridge=new Bridge(()->client,event->{},100,clock::get)){
            JsonObject login=request("connect");login.addProperty("username","offline");login.addProperty("password","offline");login.addProperty("environment","demo");login.addProperty("cacheDirectory",Files.createTempDirectory("jforex-valuation-").toString());good(bridge,login);
            JsonObject buy=estimate(bridge,Instrument.AAPLUSUSD,"buy"),sell=estimate(bridge,Instrument.AAPLUSUSD,"sell");
            check(buy.get("accountCurrency").getAsString().equals("EUR"),"account currency comes from SDK");near(buy.get("entryPrice").getAsDouble(),100.2,"buy ask");near(sell.get("entryPrice").getAsDouble(),100,"sell bid");
            near(buy.get("profitPerPriceUnit").getAsDouble(),0.9,"USD-quoted CFD in EUR account");near((95-100.2)*2*buy.get("profitPerPriceUnit").getAsDouble(),-9.36,"CFD units quantity and price movement, no lot or leverage multiplier");
            near(buy.get("expensePerPriceUnit").getAsDouble(),1/1.11,"expenses use costly inverse FX bid while P/L keeps SDK conversion");
            near((100-95)*2*sell.get("profitPerPriceUnit").getAsDouble(),9,"sell target account profit");check(buy.get("valuationTime").getAsLong()==now,"verified quote valuation timestamp");
            JsonObject jpy=estimate(bridge,Instrument.USDJPY,"buy");near(jpy.get("profitPerPriceUnit").getAsDouble(),0.006,"JPY quote conversion factor uses pip ratio");
            currency.set(JFCurrency.getInstance("USD"));near(estimate(bridge,Instrument.AAPLUSUSD,"buy").get("profitPerPriceUnit").getAsDouble(),1,"same account/quote currency is still SDK-derived");
            currency.set(JFCurrency.getInstance("EUR"));conversionStale.set(true);JsonObject stale=estimate(bridge,Instrument.AAPLUSUSD,"buy");check(stale.get("referenceOnly").getAsBoolean()&&!stale.has("valuationError")&&stale.get("profitPerPriceUnit").getAsDouble()>0&&stale.get("entryPrice").getAsDouble()==100.2&&stale.get("valuationTime").getAsLong()==now-60000,"real last FX quote supports a clearly marked reference estimate");
            conversionStale.set(false);conversionBlocked.set(true);JsonObject blocked=estimate(bridge,Instrument.AAPLUSUSD,"buy");check(!blocked.has("valuationError")&&blocked.get("profitPerPriceUnit").getAsDouble()>0&&blocked.has("valuationNotice"),"CFD SDK pip failure falls back to actual fresh FX quotes");
            check(blocked.get("valuationSource").getAsString().contains("FX quote"),"fallback provenance visible");
            currency.set(JFCurrency.getInstance("USD"));JsonObject identity=estimate(bridge,Instrument.AAPLUSUSD,"buy");near(identity.get("profitPerPriceUnit").getAsDouble(),1,"actual same currency identity independent of broken SDK pip helper");
            currency.set(JFCurrency.getInstance("EUR"));modernCurrencyMissing.set(true);check(estimate(bridge,Instrument.AAPLUSUSD,"buy").get("accountCurrency").getAsString().equals("EUR"),"legacy SDK currency accessor fallback");modernCurrencyMissing.set(false);
            int conversionsBeforeMetrics=conversionCalls.get(),fxQuotesBeforeMetrics=fxQuoteCalls.get();
            JsonObject metricsRequest=request("tradeMetrics");metricsRequest.addProperty("instrument",Instrument.AAPLUSUSD.name());metricsRequest.addProperty("side","buy");
            JsonObject money=good(bridge,metricsRequest).getAsJsonObject("result");near(money.get("balance").getAsDouble(),1000,"SDK previous-day account balance");near(money.get("equity").getAsDouble(),1100,"SDK equity");near(money.get("usedMargin").getAsDouble(),100,"SDK used margin");near(money.get("availableBalance").getAsDouble(),1000,"free margin is equity minus used margin");
            check(!money.has("marginRequired")&&!money.has("liquidationPrice")&&money.has("marginError")&&money.has("liquidationError"),"no fictitious CFD margin or standalone liquidation price");
            conversionStale.set(true);money=good(bridge,metricsRequest).getAsJsonObject("result");check(money.has("balance")&&money.has("availableBalance")&&!money.has("valuationError"),"independent balance does not request stale FX conversion");
            conversionStale.set(false);entryStale.set(true);money=good(bridge,metricsRequest).getAsJsonObject("result");check(money.has("balance")&&money.has("entryError"),"account balance readable while entry market is stale");
            entryStale.set(false);accountReadFails.set(true);money=good(bridge,metricsRequest).getAsJsonObject("result");check(money.has("balance")&&money.has("equity")&&!money.has("usedMargin")&&!money.has("availableBalance")&&money.has("accountError"),"independent SDK field failure does not invent free margin");accountReadFails.set(false);
            check(conversionCalls.get()>conversionsBeforeMetrics&&fxQuoteCalls.get()>fxQuotesBeforeMetrics,"maximum sizing reads fresh FX while independent balances survive conversion failures");
            conversionBlocked.set(false);BridgeTradingTest.metadata(Instrument.AAPLUSUSD,"marginUse",10);
            metricsRequest.addProperty("quantity",2);metricsRequest.addProperty("leverage",100);
            money=good(bridge,metricsRequest).getAsJsonObject("result");
            check(money.get("maximumQuantity").getAsString().equals("108"),"maximum respects instrument 10x rather than account 100x, opening spread, slippage and integer step");
            near(money.get("marginRequired").getAsDouble(),2*(100.2+0.05)/1.11/10,"CFD margin covers expensive executable account-currency conversion");
            check(money.has("maximumQuantityBasis")&&money.has("marginBasis")&&!money.has("marginError"),"margin and maximum estimates identify their inputs");
            equity.set(1098.9);money=good(bridge,metricsRequest).getAsJsonObject("result");
            check(money.get("maximumQuantity").getAsString().equals("107"),"expense-side FX rate lowers maximum when midpoint-funded last unit would exceed margin");equity.set(1100.0);
            metricsRequest.addProperty("quantity",1e308);money=good(bridge,metricsRequest).getAsJsonObject("result");
            check(money.get("maximumQuantity").getAsString().equals("108")&&money.has("marginError")&&!money.has("marginRequired"),"overflowing entered quantity retains independently calculated maximum and valid JSON");metricsRequest.addProperty("quantity",2);
            entryOverride.set(fake(ITick.class,(method,args)->switch(method){
                case "getTime"->now;case "getBid"->100.0;case "getAsk"->100.2;
                case "getAsks"->new double[]{100.2,100.22,100.26};case "getAskVolumes"->new double[]{.000002,.000003,.001};
                case "getBids"->new double[]{100,99.97,99.94};case "getBidVolumes"->new double[]{.000007,.000004,.001};default->null;
            }));
            money=good(bridge,metricsRequest).getAsJsonObject("result");
            check(money.get("maximumQuantity").getAsString().equals("5"),"buy depth converts million base units and excludes asks beyond five-pip bound");
            metricsRequest.addProperty("side","sell");money=good(bridge,metricsRequest).getAsJsonObject("result");
            check(money.get("maximumQuantity").getAsString().equals("11"),"sell depth uses bids and excludes bids beyond five-pip bound");
            entryOverride.set(null);metricsRequest.addProperty("side","buy");
            for(long elapsed:List.of(30001L,2000L)){
                conversionAge.set(elapsed==2000?29000:0);
                entryOverride.set(fake(ITick.class,(method,args)->switch(method){
                    case "getTime"->now;case "getBid"->100.0;case "getAsk"->100.2;
                    case "getAskVolume"->{clock.addAndGet(elapsed);yield .01;}default->null;
                }));
                money=good(bridge,metricsRequest).getAsJsonObject("result");
                check(money.has("balance")&&money.has("maximumQuantityError")&&!money.has("maximumQuantity")&&!money.has("marginRequired"),"post-depth freshness rejects expired quote/account or previously fresh FX valuation");
                check(money.get("maximumQuantityError").getAsString().contains("became stale"),"expired sizing snapshot explains failure");
                clock.set(now);conversionAge.set(0);entryOverride.set(null);
            }
            currency.set(JFCurrency.getInstance("USD"));JsonObject fxMetrics=request("tradeMetrics");fxMetrics.addProperty("instrument",Instrument.EURUSD.name());fxMetrics.addProperty("side","buy");fxMetrics.addProperty("quantity",1000);fxMetrics.addProperty("leverage",100);
            JsonObject fxMoney=good(bridge,fxMetrics).getAsJsonObject("result");
            check(fxMoney.get("maximumQuantity").getAsString().equals("10000"),"FX zero instrument override uses account leverage and base-unit liquidity/contract rules");
            near(fxMoney.get("marginRequired").getAsDouble(),1000*(1.112+0.0005)/100,"FX margin uses actual base units at account leverage");currency.set(JFCurrency.getInstance("EUR"));
            conversionStale.set(true);money=good(bridge,metricsRequest).getAsJsonObject("result");
            check(money.has("balance")&&money.has("availableBalance")&&money.has("maximumQuantityError")&&!money.has("maximumQuantity"),"stale sizing FX preserves independently read balances");conversionStale.set(false);
            metricsRequest.addProperty("leverage",5);money=good(bridge,metricsRequest).getAsJsonObject("result");
            check(money.has("maximumQuantityError")&&!money.has("maximumQuantity"),"cannot size with leverage unavailable to fixed account");
            BridgeTradingTest.metadata(Instrument.AAPLUSUSD,"marginUse",0);
            check(TradeSizing.maximum(1000,10,.25,1,1,1000,10000).equals("97"),"free-margin capacity includes opening costs");
            check(TradeSizing.maximum(1000,1,0,.01,.01,10000,3.147).equals("3.14"),"visible liquidity cap rounds down to fractional instrument step");
            check(TradeSizing.maximum(1000,1,0,1,1,12,10000).equals("12"),"instrument maximum caps funded liquidity");
            check(TradeSizing.maximum(9,10,0,1,1,1000,10000).equals("0"),"subminimum funded size is zero");
            check(TradeSizing.maximum(-1,10,0,1,1,1000,10000).equals("0"),"negative free margin cannot open new exposure");
            check(TradeSizing.maximum(1,.1,0,.01,.01,1000,10000).equals("10"),"decimal boundary does not round one lot beyond affordability");
            conversionBlocked.set(false);entryStale.set(true);JsonObject last=estimate(bridge,Instrument.AAPLUSUSD,"buy");
            check(last.get("referenceOnly").getAsBoolean()&&last.get("valuationTime").getAsLong()==now-60000&&last.get("entryPrice").getAsDouble()==100.2,"stale real entry supports planning without pretending to be executable");
            entryStale.set(false);currency.set(JFCurrency.getInstance("USD"));conversionAge.set(48*3_600_000L);
            BridgeTradingTest.metadata(Instrument.BMWDEEUR,"amountPerContract",1);BridgeTradingTest.metadata(Instrument.BMWDEEUR,"minTradeAmount",1);BridgeTradingTest.metadata(Instrument.BMWDEEUR,"tradeAmountIncrement",1);BridgeTradingTest.metadata(Instrument.BMWDEEUR,"maxTradeAmount",10000);
            JsonObject closed=estimate(bridge,Instrument.BMWDEEUR,"buy");
            check(closed.get("referenceOnly").getAsBoolean()&&closed.get("valuationTime").getAsLong()==now-72*3_600_000L&&closed.get("valuationNotice").getAsString().contains("planning only"),"closed EUR-quoted CFD retains actual oldest entry/FX date and planning notice");
            near(closed.get("entryPrice").getAsDouble(),14.82,"closed CFD buy uses genuine last ask");near(closed.get("profitPerPriceUnit").getAsDouble(),1.111,"closed EUR CFD in USD account uses actual last EURUSD quotes");
            near((14-14.82)*1000*closed.get("profitPerPriceUnit").getAsDouble(),-911.02,"closed CFD stop estimate uses base units and real account conversion");
            near(estimate(bridge,Instrument.BMWDEEUR,"sell").get("entryPrice").getAsDouble(),14.8,"closed CFD sell uses genuine last bid");
            JsonObject preview=bridge.handle(BridgeTradingTest.trade("tradePreview",Instrument.BMWDEEUR,"buy",1000,14,16));
            check(!preview.get("ok").getAsBoolean()&&preview.toString().contains("fresh JForex Bid/Ask"),"reference quote never makes a closed CFD executable");
            conversionAge.set(15*86_400_000L);JsonObject missingFx=estimate(bridge,Instrument.BMWDEEUR,"buy");
            check(missingFx.has("valuationError")&&missingFx.get("profitPerPriceUnit").getAsDouble()==0&&missingFx.get("entryPrice").getAsDouble()==14.82&&missingFx.get("valuationTime").getAsLong()==now-72*3_600_000L,"unavailable recent FX keeps entry/bps while account amount stays unknown");
            conversionAge.set(0);entryOverride.set(tick(100,100.2,now-15*86_400_000L));JsonObject tooOld=request("tradeEstimate");tooOld.addProperty("instrument",Instrument.AAPLUSUSD.name());tooOld.addProperty("side","buy");check(!bridge.handle(tooOld).get("ok").getAsBoolean(),"reference quotes older than 14 days stay unavailable");
            check(engineCalls.get()==0&&conversionCalls.get()>0,"estimate is read-only SDK valuation");check(subscribed.isEmpty(),"temporary quote/conversion subscriptions released");
        }
        System.out.println("JForex SDK account-currency trade valuation tests passed");
    }
}
