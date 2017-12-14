<div class="container-fluid"><div class="row">
<div class="col-sm-7">{{Avatar .Message.Author}}</div>
<div class="col-sm-5">
<div class="row">
<div class="col-xs-8">
<a href="/post?id={{.Message.Key}}">{{RenderJSTime .Message.Timestamp}}</a>
</div>
<div class="col-xs-4" style="text-align: right;">
{{$votes := GetVotes .Message.Key}}{{len $votes}} Votes
</div>
</div>
{{if .Content.Project.IsMessage}}<a href="/repo?id={{urlquery .Content.Project}}">{{.Content.Project}}</a>{{end}}

</div>
</div></div>
<div>
{{Markdown .Content.Text}}
</div>
<a href="/thread?id={{.Message.Key}}">View comments</a><br>
