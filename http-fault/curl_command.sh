#!/usr/bin/env bash

# 随机生成一个 traceparent（示例）
TRACEPARENT="00-$(uuidgen | tr -d '-')-$(printf '%016x' $RANDOM)-01"
# 一个简单的 tracestate 示例
TRACESTATE="fault=1"

# 下面的参数你可以根据需要改成脚本生成或固定值
IN_DATE="2015-04-12"
OUT_DATE="2015-04-15"
LAT="38.0235"
LON="-122.095"

curl -v -X GET "http://10.102.252.7:5000/hotels?inDate=${IN_DATE}&outDate=${OUT_DATE}&lat=${LAT}&lon=${LON}" \
  -H "traceparent: ${TRACEPARENT}" \
  -H "tracestate: ${TRACESTATE}" \
  -H "Accept: application/json" \
  -w "\nHTTP STATUS: %{http_code}\n"

