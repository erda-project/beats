#!/bin/bash -e

if [[ $DICE_IS_EDGE == 'true' ]]; then
    export OUTPUT_TERMINUS_HOSTS=$COLLECTOR_PUBLIC_URL
else
    export OUTPUT_TERMINUS_HOSTS=http://$COLLECTOR_ADDR
fi
echo "${OUTPUT_TERMINUS_HOSTS}"

# migrate old version registry file
home_registry='/data/spot/filebeat/data/registry'
data_json_file=$home_registry'/log_tail/data.json'
dst=$home_registry'/filebeat/'
if [ -f "$data_json_file" ]; then
	echo "copy data.json to dir filebeat"
	cp $data_json_file $dst
	mv $data_json_file /tmp/
fi

cfg_path='conf/filebeat.yml'
if [ "$FILEBEAT_CONFIG_PATH" != '' ]; then
	cfg_path=$FILEBEAT_CONFIG_PATH
fi

./filebeat -c "$cfg_path" -e --httpprof localhost:6060
