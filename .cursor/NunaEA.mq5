#property copyright "Algoview.co"
#property link      "https://www.algoview.co"
#property version   "1.00"
#property strict
#property description "Nuna EA is a fully automated Expert Advisor for NASDAQ. It is based on a combination of trend-following and mean-reversion strategies."

// Include necessary libraries
#include <Trade/Trade.mqh>
#include <Trade/SymbolInfo.mqh>
#include <Trade/PositionInfo.mqh>

// Input parameters
input int      MagicNumber = 12345;  // Magic number for orders
input double   LotSize = 0.01;       // Fixed lot size
input int      MAPeriod = 50;        // Moving Average period
input int      RSIPeriod = 14;       // RSI period
input double   RSI_Upper = 70.0;     // RSI overbought level
input double   RSI_Lower = 30.0;     // RSI oversold level
input int      StopLoss = 500;       // Stop loss in points
input int      TakeProfit = 1000;    // Take profit in points

// Global variables
CTrade          trade;
CSymbolInfo     symbol;
CPositionInfo   position;

//+------------------------------------------------------------------+
//| Expert initialization function                                   |
//+------------------------------------------------------------------+
int OnInit()
{
    //--- Initialize trade object
    trade.SetExpertMagicNumber(MagicNumber);
    trade.SetDeviationInPoints(10);
    trade.SetTypeFillingBySymbol(_Symbol);

    //--- Initialize symbol info object
    symbol.Name(_Symbol);

    //--- Set Expert Advisor name
    string short_name = "NunaEA";
    ChartSetString(0, CHART_COMMENT, short_name);
    Comment(short_name);

    //--- Done
    return (INIT_SUCCEEDED);
}

//+------------------------------------------------------------------+
//| Expert deinitialization function                                 |
//+------------------------------------------------------------------+
void OnDeinit(const int reason)
{
    //--- Remove comment from the chart
    ChartSetString(0, CHART_COMMENT, "");
}

//+------------------------------------------------------------------+
//| Expert tick function                                             |
//+------------------------------------------------------------------+
void OnTick()
{
    //--- Get the latest prices
    MqlTick latest_price;
    if (!SymbolInfoTick(_Symbol, latest_price))
    {
        return;
    }

    //--- Check for open positions
    if (position.Select(_Symbol))
    {
        //--- No new trades if a position is already open
        return;
    }

    //--- Get indicator values
    double ma_value = iMA(_Symbol, 0, MAPeriod, 0, MODE_SMA, PRICE_CLOSE, 0);
    double rsi_value = iRSI(_Symbol, 0, RSIPeriod, PRICE_CLOSE, 0);

    //--- Trading logic
    //--- Buy condition: Price is above MA and RSI is not overbought
    if (latest_price.ask > ma_value && rsi_value < RSI_Upper)
    {
        //--- Calculate Stop Loss and Take Profit levels
        double sl = latest_price.ask - StopLoss * _Point;
        double tp = latest_price.ask + TakeProfit * _Point;

        //--- Open a buy order
        trade.Buy(LotSize, _Symbol, latest_price.ask, sl, tp, "NunaEA Buy");
    }
    //--- Sell condition: Price is below MA and RSI is not oversold
    else if (latest_price.bid < ma_value && rsi_value > RSI_Lower)
    {
        //--- Calculate Stop Loss and Take Profit levels
        double sl = latest_price.bid + StopLoss * _Point;
        double tp = latest_price.bid - TakeProfit * _Point;

        //--- Open a sell order
        trade.Sell(LotSize, _Symbol, latest_price.bid, sl, tp, "NunaEA Sell");
    }
}
