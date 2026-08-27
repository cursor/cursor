//+------------------------------------------------------------------+
//|                                            Strategy_Breakout.mqh |
//|                               Copyright 2026, google.com.jules   |
//|                         Donchian/High-Low Breakout Strategy      |
//+------------------------------------------------------------------+
#property copyright "Copyright 2026, google.com.jules"
#property strict

#include <Trade\Trade.mqh>

class Strategy_Breakout
{
private:
   CTrade  m_trade;
   int     m_lookbackBars;
   double  m_lotSize;
   int     m_stopLoss;
   int     m_takeProfit;
   ulong   m_magicNumber;

public:
   Strategy_Breakout() : m_lookbackBars(20), m_lotSize(0.01), m_stopLoss(150), m_takeProfit(300), m_magicNumber(3637102) {}

   void Init(int lookback = 20, double lotSize = 0.01, int sl = 150, int tp = 300, ulong magic = 3637102)
   {
      m_lookbackBars = lookback;
      m_lotSize = lotSize;
      m_stopLoss = sl;
      m_takeProfit = tp;
      m_magicNumber = magic;
      m_trade.SetExpertMagicNumber(m_magicNumber);
   }

   void Run()
   {
      static datetime lastBarTime = 0;
      datetime currentBarTime = iTime(_Symbol, _Period, 0);
      if(currentBarTime == lastBarTime) return;
      lastBarTime = currentBarTime;

      int totalPositions = 0;
      for(int i = PositionsTotal() - 1; i >= 0; i--)
      {
         if(PositionGetSymbol(i) == _Symbol && PositionGetInteger(POSITION_MAGIC) == m_magicNumber)
            totalPositions++;
      }
      if(totalPositions > 0) return;

      int highestBar = iHighest(_Symbol, _Period, MODE_HIGH, m_lookbackBars, 1);
      int lowestBar  = iLowest(_Symbol, _Period, MODE_LOW, m_lookbackBars, 1);
      if(highestBar < 0 || lowestBar < 0) return;

      double highestPrice = iHigh(_Symbol, _Period, highestBar);
      double lowestPrice  = iLow(_Symbol, _Period, lowestBar);
      double currentAsk   = SymbolInfoDouble(_Symbol, SYMBOL_ASK);
      double currentBid   = SymbolInfoDouble(_Symbol, SYMBOL_BID);

      if(currentAsk > highestPrice)
      {
         double sl = (m_stopLoss > 0) ? currentAsk - m_stopLoss * _Point : 0;
         double tp = (m_takeProfit > 0) ? currentAsk + m_takeProfit * _Point : 0;
         m_trade.Buy(m_lotSize, _Symbol, currentAsk, sl, tp, "Strategy_Breakout Buy");
      }
      else if(currentBid < lowestPrice)
      {
         double sl = (m_stopLoss > 0) ? currentBid + m_stopLoss * _Point : 0;
         double tp = (m_takeProfit > 0) ? currentBid - m_takeProfit * _Point : 0;
         m_trade.Sell(m_lotSize, _Symbol, currentBid, sl, tp, "Strategy_Breakout Sell");
      }
   }
};
