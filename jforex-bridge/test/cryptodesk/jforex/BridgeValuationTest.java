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
    static ITick tick(double bid,double ask,long at){return fake(ITick.class,(method,args)->switch(method){case "getTime"->at;case "getBid"->bid;case "getAsk"->ask;default->null;});}
    static void run() throws Exception {
        long now=System.currentTimeMillis();
        AtomicBoolean connected=new AtomicBoolean(),conversionBlocked=new AtomicBoolean(),conversionStale=new AtomicBoolean(),entryStale=new AtomicBoolean(),accountReadFails=new AtomicBoolean(),modernCurrencyMissing=new AtomicBoolean();
        AtomicReference<ICurrency> currency=new AtomicReference<>(JFCurrency.getInstance("EUR"));
        AtomicInteger conversionCalls=new AtomicInteger(),engineCalls=new AtomicInteger(),fxQuoteCalls=new AtomicInteger();
        Set<Instrument> subscribed=new HashSet<>();
        IHistory history=fake(IHistory.class,(method,args)->{
            if(!method.equals("getLastTick"))return null;
            Instrument instrument=(Instrument)args[0];
            if (!instrument.equals(Instrument.AAPLUSUSD)) fxQuoteCalls.incrementAndGet();
            long at=now-(entryStale.get()&&instrument.equals(Instrument.AAPLUSUSD)||conversionStale.get()&&instrument.equals(Instrument.EURUSD)?60000:0);
            if(instrument.equals(Instrument.AAPLUSUSD))return tick(100,100.2,at);
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
            case "getBalance"->1000.0;case "getEquity"->1100.0;case "getUsedMargin"->{if(accountReadFails.get())throw new RuntimeException("explicit SDK failure");yield 100.0;}default->null;});
        IContext context=fake(IContext.class,(method,args)->switch(method){
            case "getHistory"->history;case "getUtils"->utils;case "getAccount"->account;
            case "getEngine"->{engineCalls.incrementAndGet();throw new AssertionError("estimate must not access order engine");}
            case "getSubscribedInstruments"->new HashSet<>(subscribed);
            case "setSubscribedInstruments"->{subscribed.clear();subscribed.addAll((Set<Instrument>)args[0]);yield null;}
            default->null;
        });
        IClient client=fake(IClient.class,(method,args)->switch(method){
            case "isConnected"->connected.get();case "connect"->{connected.set(true);yield null;}
            case "getAvailableInstruments"->Set.of(Instrument.EURUSD,Instrument.USDJPY,Instrument.EURJPY,Instrument.AAPLUSUSD);
            case "getSubscribedInstruments"->new HashSet<>(subscribed);case "unsubscribeInstruments"->{subscribed.removeAll((Set<Instrument>)args[0]);yield null;}
            case "startStrategy"->{((IStrategy)args[0]).onStart(context);yield 1L;}default->null;
        });
        try(Bridge bridge=new Bridge(()->client,event->{},100,()->now)){
            JsonObject login=request("connect");login.addProperty("username","offline");login.addProperty("password","offline");login.addProperty("environment","demo");login.addProperty("cacheDirectory",Files.createTempDirectory("jforex-valuation-").toString());good(bridge,login);
            JsonObject buy=estimate(bridge,Instrument.AAPLUSUSD,"buy"),sell=estimate(bridge,Instrument.AAPLUSUSD,"sell");
            check(buy.get("accountCurrency").getAsString().equals("EUR"),"account currency comes from SDK");near(buy.get("entryPrice").getAsDouble(),100.2,"buy ask");near(sell.get("entryPrice").getAsDouble(),100,"sell bid");
            near(buy.get("profitPerPriceUnit").getAsDouble(),0.9,"USD-quoted CFD in EUR account");near((95-100.2)*2*buy.get("profitPerPriceUnit").getAsDouble(),-9.36,"CFD units quantity and price movement, no lot or leverage multiplier");
            near((100-95)*2*sell.get("profitPerPriceUnit").getAsDouble(),9,"sell target account profit");check(buy.get("valuationTime").getAsLong()==now,"verified quote valuation timestamp");
            JsonObject jpy=estimate(bridge,Instrument.USDJPY,"buy");near(jpy.get("profitPerPriceUnit").getAsDouble(),0.006,"JPY quote conversion factor uses pip ratio");
            currency.set(JFCurrency.getInstance("USD"));near(estimate(bridge,Instrument.AAPLUSUSD,"buy").get("profitPerPriceUnit").getAsDouble(),1,"same account/quote currency is still SDK-derived");
            currency.set(JFCurrency.getInstance("EUR"));conversionStale.set(true);JsonObject stale=estimate(bridge,Instrument.AAPLUSUSD,"buy");check(stale.has("valuationError")&&stale.get("profitPerPriceUnit").getAsDouble()==0&&stale.get("entryPrice").getAsDouble()==100.2,"stale conversion keeps entry, never invents rate");
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
            check(conversionCalls.get()==conversionsBeforeMetrics&&fxQuoteCalls.get()==fxQuotesBeforeMetrics,"account metrics performs no FX-helper or FX-quote reads");
            conversionBlocked.set(false);entryStale.set(true);JsonObject r=request("tradeEstimate");r.addProperty("instrument",Instrument.AAPLUSUSD.name());r.addProperty("side","buy");check(!bridge.handle(r).get("ok").getAsBoolean(),"stale entry not an executable quote");
            check(engineCalls.get()==0&&conversionCalls.get()>0,"estimate is read-only SDK valuation");check(subscribed.isEmpty(),"temporary quote/conversion subscriptions released");
        }
        System.out.println("JForex SDK account-currency trade valuation tests passed");
    }
}
