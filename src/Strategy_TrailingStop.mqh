//+------------------------------------------------------------------+
//|                                        Strategy_TrailingStop.mqh |
//|                               Copyright 2026, google.com.jules   |
//|                         Dynamic Trailing Stop Strategy Module    |
//+------------------------------------------------------------------+
#property copyright "Copyright 2026, google.com.jules"
#property strict

#include <Trade\Trade.mqh>

class Strategy_TrailingStop
{
private:
   CTrade  m_trade;
   int     m_trailingStopPoints;
   int     m_trailingStepPoints;

public:
   Strategy_TrailingStop() : m_trailingStopPoints(100), m_trailingStepPoints(20) {}

   void Init(int trailingStop = 100, int trailingStep = 20)
   {
      m_trailingStopPoints = trailingStop;
      m_trailingStepPoints = trailingStep;
   }

   void Run()
   {
      if(m_trailingStopPoints <= 0) return;

      for(int i = PositionsTotal() - 1; i >= 0; i--)
      {
         if(PositionGetSymbol(i) == _Symbol)
         {
            ulong ticket = PositionGetInteger(POSITION_TICKET);
            ENUM_POSITION_TYPE type = (ENUM_POSITION_TYPE)PositionGetInteger(POSITION_TYPE);
            double currentSL = PositionGetDouble(POSITION_SL);
            double openPrice = PositionGetDouble(POSITION_PRICE_OPEN);

            if(type == POSITION_TYPE_BUY)
            {
               double bid = SymbolInfoDouble(_Symbol, SYMBOL_BID);
               if(bid - openPrice > m_trailingStopPoints * _Point)
               {
                  double newSL = bid - m_trailingStopPoints * _Point;
                  if(newSL > currentSL + m_trailingStepPoints * _Point)
                  {
                     m_trade.PositionModify(ticket, newSL, PositionGetDouble(POSITION_TP));
                  }
               }
            }
            else if(type == POSITION_TYPE_SELL)
            {
               double ask = SymbolInfoDouble(_Symbol, SYMBOL_ASK);
               if(openPrice - ask > m_trailingStopPoints * _Point)
               {
                  double newSL = ask + m_trailingStopPoints * _Point;
                  if(currentSL == 0 || newSL < currentSL - m_trailingStepPoints * _Point)
                  {
                     m_trade.PositionModify(ticket, newSL, PositionGetDouble(POSITION_TP));
                  }
               }
            }
         }
      }
   }
};
